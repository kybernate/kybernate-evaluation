# Makefile for Kybernate

# Image Registry (change to your registry)
REGISTRY ?= localhost:32000
TAG ?= latest

# Binaries
BIN_DIR := bin
SHIM_BIN := containerd-shim-kybernate-v1
AGENT_BIN := kybernate-agent
OPERATOR_BIN := kybernate-operator
PLUGIN_BIN := kybernate-device-plugin

.PHONY: all build clean test image deploy

all: build

build:
	@echo "Building binaries..."
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(SHIM_BIN) ./cmd/containerd-shim-kybernate-v1
	go build -o $(BIN_DIR)/$(AGENT_BIN) ./cmd/kybernate-agent
	go build -o $(BIN_DIR)/$(OPERATOR_BIN) ./cmd/kybernate-operator
	go build -o $(BIN_DIR)/$(PLUGIN_BIN) ./cmd/kybernate-device-plugin

clean:
	rm -rf $(BIN_DIR)

test:
	go test ./pkg/... ./cmd/...

# Code Generation (Protobuf & CRDs)
generate:
	protoc --go_out=. --go-grpc_out=. pkg/api/agent/v1/agent.proto
	# controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./pkg/api/..."

# Docker Images
image: image-agent image-operator image-plugin

image-agent:
	docker build -t $(REGISTRY)/kybernate-agent:$(TAG) -f build/Dockerfile.agent .
	docker push $(REGISTRY)/kybernate-agent:$(TAG)

image-operator:
	docker build -t $(REGISTRY)/kybernate-operator:$(TAG) -f build/Dockerfile.operator .
	docker push $(REGISTRY)/kybernate-operator:$(TAG)

image-plugin:
	docker build -t $(REGISTRY)/kybernate-device-plugin:$(TAG) -f build/Dockerfile.plugin .
	docker push $(REGISTRY)/kybernate-device-plugin:$(TAG)

# Deployment
deploy:
	kubectl apply -f manifests/crd/
	kubectl apply -f manifests/operator/
	kubectl apply -f manifests/agent/
	kubectl apply -f manifests/device-plugin/
