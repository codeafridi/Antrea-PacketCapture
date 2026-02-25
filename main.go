package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	annotationKey   = "tcpdump.antrea.io"
	captureDir      = "/captures"
	captureFileTmpl = "capture-%s.pcap"
)

type capture struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

type controller struct {
	client    *kubernetes.Clientset
	nodeName  string
	captures  map[string]*capture
	mu        sync.Mutex
	namespace string
}

func newController(client *kubernetes.Clientset, nodeName string) *controller {
	return &controller{
		client:   client,
		nodeName: nodeName,
		captures: make(map[string]*capture),
	}
}

func (c *controller) onAddOrUpdate(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	if pod.Spec.NodeName != c.nodeName {
		return
	}
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	ann, has := pod.Annotations[annotationKey]

	c.mu.Lock()
	defer c.mu.Unlock()

	if has {
		if _, running := c.captures[key]; running {
			return
		}
		n, err := strconv.Atoi(ann)
		if err != nil || n <= 0 {
			log.Printf("invalid value for %s on pod %s: %q", annotationKey, key, ann)
			return
		}
		if err := os.MkdirAll(captureDir, 0o755); err != nil {
			log.Printf("failed to create capture dir: %v", err)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		file := filepath.Join(captureDir, fmt.Sprintf(captureFileTmpl, pod.Name))
		args := []string{
			"-i", "any",
			"-C", "1",
			"-W", strconv.Itoa(n),
			"-w", file,
		}
		cmd := exec.CommandContext(ctx, "tcpdump", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("failed to start tcpdump for pod %s: %v", key, err)
			cancel()
			return
		}
		log.Printf("started capture for pod %s with N=%d", key, n)
		c.captures[key] = &capture{cmd: cmd, cancel: cancel}
		go func(k string, cp *capture) {
			err := cp.cmd.Wait()
			if err != nil {
				log.Printf("tcpdump exited for pod %s: %v", k, err)
			} else {
				log.Printf("tcpdump exited for pod %s", k)
			}
		}(key, c.captures[key])
	} else {
		if cap, running := c.captures[key]; running {
			log.Printf("stopping capture for pod %s", key)
			cap.cancel()
			_ = cap.cmd.Process.Signal(syscall.SIGINT)
			delete(c.captures, key)
			_ = c.cleanupFiles(pod)
		}
	}
}

func (c *controller) onDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	}
	if pod.Spec.NodeName != c.nodeName {
		return
	}
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	c.mu.Lock()
	defer c.mu.Unlock()

	if cap, running := c.captures[key]; running {
		log.Printf("pod %s deleted, stopping capture", key)
		cap.cancel()
		_ = cap.cmd.Process.Signal(syscall.SIGINT)
		delete(c.captures, key)
	}
	_ = c.cleanupFiles(pod)
}

func (c *controller) cleanupFiles(pod *corev1.Pod) error {
	pattern := filepath.Join(captureDir, fmt.Sprintf(captureFileTmpl, pod.Name))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, f := range matches {
		if err := os.Remove(f); err != nil {
			log.Printf("failed to remove %s: %v", f, err)
		} else {
			log.Printf("removed %s", f)
		}
	}
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		log.Fatal("NODE_NAME env var must be set")
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("failed to get in-cluster config: %v", err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create clientset: %v", err)
	}

	sharedFactory := informers.NewSharedInformerFactoryWithOptions(
		client,
		time.Minute,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
			opts.LabelSelector = labels.Everything().String()
		}),
	)

	podInformer := sharedFactory.Core().V1().Pods().Informer()

	ctrl := newController(client, nodeName)

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.onAddOrUpdate,
		UpdateFunc: func(oldObj, newObj interface{}) { ctrl.onAddOrUpdate(newObj) },
		DeleteFunc: ctrl.onDelete,
	})

	stopCh := make(chan struct{})
	defer close(stopCh)

	go sharedFactory.Start(stopCh)

	if !cache.WaitForCacheSync(stopCh, podInformer.HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	log.Printf("capture controller running on node %s", nodeName)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutting down")
}

