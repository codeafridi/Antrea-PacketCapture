## Poor-man’s Antrea PacketCapture (DaemonSet + tcpdump)

This repo is a small Kubernetes controller that runs **one Pod per Node** (DaemonSet).  
It watches Pods on the **same Node** and starts / stops packet capture using `tcpdump` based on a Pod annotation.

### What I built (simple)

- **Controller (Go)**: watches Pods on the local Node.
- **Trigger**: add annotation **`tcpdump.antrea.io: "<N>"`** to a Pod.
- **Action**: the node-local controller starts `tcpdump` for that Pod and writes pcaps to `/captures`.
- **Stop**: remove the annotation; controller stops `tcpdump` and deletes the capture files.

### How capture works

When the annotation is present, the controller runs:

`tcpdump -i any -C 1 -W <N> -w /captures/capture-<pod-name>.pcap`

- `-C 1`: rotate every ~1MB
- `-W <N>`: keep at most `<N>` files
- Output directory is `/captures` (mounted from the host so files are easy to inspect / copy)

### Repo files

- **`main.go`**: controller source code
- **`Dockerfile`**: Ubuntu 24.04 image with `tcpdump` + controller binary
- **`deploy-daemonset.yaml`**: RBAC + DaemonSet
- **`test-pod.yaml`**: traffic generator Pod
- **`kind-config.yaml`**: kind cluster config (default CNI disabled)
- **`Makefile`**: build image + load into kind

### Prerequisites

Install these locally:

- `docker`
- `kind`
- `kubectl`
- `helm`
- `go` (1.22+)

### 1) Create kind cluster (disable default CNI)

```bash
kind create cluster --name antrea-pcap --config kind-config.yaml
kubectl get nodes
```

### 2) Install Antrea (Helm)

```bash
helm repo add antrea https://charts.antrea.io
helm repo update
helm install antrea antrea/antrea -n kube-system

kubectl -n kube-system get pods
```

Wait until Antrea Pods are `Running`.

### 3) Build image and load into kind

```bash
make kind-load
```

### 4) Deploy the capture controller (DaemonSet)

```bash
kubectl apply -f deploy-daemonset.yaml
kubectl -n kube-system get pods -l app=capture-controller -o wide
```

### 5) Deploy the test Pod (generates traffic)

```bash
kubectl apply -f test-pod.yaml
kubectl get pod traffic-generator -o wide
```

### 6) Start capture by annotating the test Pod

```bash
kubectl annotate pod traffic-generator tcpdump.antrea.io="5" --overwrite
```

Find the capture controller Pod on the **same node** (PowerShell):

```powershell
$NODE = kubectl get pod traffic-generator -o jsonpath='{.spec.nodeName}'
$CAP_POD = kubectl -n kube-system get pod -l app=capture-controller -o jsonpath="{.items[?(@.spec.nodeName=='$NODE')].metadata.name}"
echo $NODE
echo $CAP_POD
```

Check that pcap files are being created:

```bash
kubectl -n kube-system exec $CAP_POD -- sh -c "ls -l /captures"
```

### 7) Stop capture and verify cleanup

Remove the annotation (this should stop `tcpdump` and delete the files):

```bash
kubectl annotate pod traffic-generator tcpdump.antrea.io-
kubectl -n kube-system exec $CAP_POD -- sh -c "ls -l /captures"
```

## Submission deliverables (how I generated them)

After the Pod is running and annotated, run the following and save outputs in this repo:

- **`pod-describe.txt`**

```bash
kubectl describe pod traffic-generator > pod-describe.txt
```

- **`pods.txt`**

```bash
kubectl get pods -A > pods.txt
```

- **`capture-files.txt`** (run while capture is active and at least one file exists)

```bash
kubectl -n kube-system exec $CAP_POD -- sh -c "ls -l /captures" > capture-files.txt
```

- **pcap copy + `capture-output.txt`**

```bash
kubectl -n kube-system cp "$CAP_POD":/captures/capture-traffic-generator.pcap ./capture-traffic-generator.pcap
tcpdump -r capture-traffic-generator.pcap > capture-output.txt
```

That’s it — the controller is intentionally small and easy to read, and the steps above reproduce the full “on-demand capture by annotation” flow.

