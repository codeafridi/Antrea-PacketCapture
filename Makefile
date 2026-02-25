IMAGE_NAME ?= capture-controller:latest

.PHONY: build
build:
	GOOS=linux GOARCH=amd64 go build -o capture-controller main.go

.PHONY: image
image: build
	docker build -t $(IMAGE_NAME) .

.PHONY: kind-load
kind-load: image
	kind load docker-image $(IMAGE_NAME)

# this is the Makefile to build the image and load it into kind