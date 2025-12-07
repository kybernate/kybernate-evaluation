# Project Structure & Scaffolding

This document defines the directory structure and module layout for the Kybernate repository.

## Directory Layout

```text
kybernate/
├── go.mod                  # Go module definition (e.g. module github.com/kybernate/kybernate)
├── go.sum
├── Makefile                # Build automation (build, test, image, deploy)
├── README.md               # Project documentation
├── bin/                    # Compiled binaries (gitignored)
├── cmd/                    # Main applications
│   ├── containerd-shim-kybernate-v1/
│   │   └── main.go         # Shim entrypoint
│   ├── kybernate-agent/
│   │   └── main.go         # Node Agent entrypoint
│   ├── kybernate-operator/
│   │   └── main.go         # K8s Operator entrypoint
│   ├── kybernate-sidecar/
│   │   └── main.go         # Sidecar helper entrypoint
│   └── kybernate-device-plugin/
│       └── main.go         # Device Plugin entrypoint
├── pkg/                    # Library code
│   ├── api/                # gRPC & CRD definitions
│   │   ├── agent/          # Agent gRPC proto
│   │   └── v1alpha1/       # CRD Go structs (kubebuilder)
│   ├── checkpoint/         # Checkpoint logic
│   │   ├── cuda/           # CUDA API wrapper
│   │   └── criu/           # CRIU wrapper
│   ├── runtime/            # Shim logic & Containerd interaction
│   ├── storage/            # Storage Tiering (S3, NVMe, RAM)
│   └── topology/           # GPU Topology & Device Plugin logic
├── manifests/              # Kubernetes YAMLs
│   ├── crd/                # Custom Resource Definitions
│   ├── operator/           # Operator Deployment
│   ├── agent/              # Node Agent DaemonSet
│   └── device-plugin/      # Device Plugin DaemonSet
├── hack/                   # Build scripts & tools
│   └── update-codegen.sh   # Script to generate K8s client code
└── test/                   # End-to-end tests & workloads
    └── e2e/
```

## Go Module

Initialize the module:
```bash
go mod init github.com/kybernate/kybernate
```

## Key Dependencies

- `k8s.io/client-go`: Kubernetes API interaction.
- `k8s.io/apimachinery`: K8s types.
- `sigs.k8s.io/controller-runtime`: Operator framework.
- `google.golang.org/grpc`: RPC communication.
- `github.com/containerd/containerd`: Containerd shim API.
- `github.com/NVIDIA/go-nvml`: GPU discovery & metrics.
