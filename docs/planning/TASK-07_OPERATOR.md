# Task 07: Kybernate Operator Implementation

**Status**: Planned
**Phase**: 2
**Abhängigkeiten**: Task 06 (GPU Checkpoint)
**Erstellt**: 2025-12-03

## Ziel

Implementierung eines Kubernetes Operators, der GPU-Checkpoint/Restore über die containerd gRPC API ermöglicht - ohne die Limitierungen des Shim-Ansatzes.

## Hintergrund

In Phase 1 (Task 06) haben wir festgestellt:
- ✅ GPU-Checkpoint funktioniert via `ctr tasks checkpoint` mit CRIU CUDA-Plugin
- ❌ Shim-Integration für GPU scheitert an containerd BinaryName-Limitation
- ❌ CRI CheckpointContainer API nicht in containerd implementiert
- ❌ Manueller `criu restore` hat Mount-Bug, `runc restore` erfordert komplexes Setup

**Lösung**: Operator der containerd gRPC API direkt nutzt.

## Voraussetzungen

- Go 1.21+
- Kubebuilder 3.x
- containerd 1.7+ (MicroK8s v1.34 erfüllt dies)
- CRIU 4.x mit cuda_plugin.so

## Schritte

### Schritt 1: Kubebuilder Projekt Setup

```bash
# Neues Operator-Projekt
mkdir -p operator && cd operator
kubebuilder init --domain kybernate.io --repo github.com/kybernate/operator

# CRDs erstellen
kubebuilder create api --group kybernate --version v1alpha1 --kind KybernateCheckpoint
kubebuilder create api --group kybernate --version v1alpha1 --kind KybernateRestore
kubebuilder create api --group kybernate --version v1alpha1 --kind KybernateWorkload
```

**Erwartetes Ergebnis**: Projekt-Skeleton mit CRD-Types

### Schritt 2: CRD Schema Definition

Definiere die API-Types basierend auf `docs/design/07_OPERATOR_DESIGN.md`:

- `KybernateCheckpointSpec`: podName, containerName, leaveRunning, storage, gpu
- `KybernateRestoreSpec`: checkpointRef, targetPod, gpu
- `KybernateWorkloadSpec`: template, suspend, scaleToZero, activator

**Erwartetes Ergebnis**: Vollständige CRD-Schemas

### Schritt 3: containerd Client Integration

```go
// pkg/containerd/client.go
package containerd

import (
    "github.com/containerd/containerd"
)

type Client struct {
    client *containerd.Client
}

func NewClient(socket string) (*Client, error) {
    c, err := containerd.New(socket)
    if err != nil {
        return nil, err
    }
    return &Client{client: c}, nil
}

func (c *Client) Checkpoint(ctx context.Context, containerID string, opts CheckpointOptions) (string, error) {
    // Implementation
}

func (c *Client) Restore(ctx context.Context, checkpointImage string, opts RestoreOptions) (string, error) {
    // Implementation
}
```

**Erwartetes Ergebnis**: Funktionierender containerd-Client

### Schritt 4: Checkpoint Controller

Implementiere den Reconciler für `KybernateCheckpoint`:

1. Pod anhand von `podName` finden
2. Container-ID aus Pod-Status extrahieren
3. containerd Client: `task.Checkpoint()` aufrufen
4. Status aktualisieren

**Test**:
```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateCheckpoint
metadata:
  name: test-checkpoint
spec:
  podName: busybox-counter
  leaveRunning: true
```

**Erwartetes Ergebnis**: Checkpoint wird erstellt, Status zeigt `Completed`

### Schritt 5: Restore Controller

Implementiere den Reconciler für `KybernateRestore`:

1. Checkpoint-Image laden
2. Neuen Container mit `containerd.WithCheckpoint()` erstellen
3. Task starten
4. Status aktualisieren

**Test**:
```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateRestore
metadata:
  name: test-restore
spec:
  checkpointRef: test-checkpoint
  targetPod:
    name: busybox-restored
```

**Erwartetes Ergebnis**: Container restored, Counter läuft weiter

### Schritt 6: GPU Integration

Erweitere Controllers um GPU-Support:

1. `CRIU_LIBS` Environment Variable setzen
2. nvidia RuntimeClass in Pod-Spec
3. GPU-Verfügbarkeitsprüfung

**Test**: GPU-Workload Checkpoint/Restore

### Schritt 7: E2E Tests

```bash
# Test-Suite
./tests/phase2/
├── test-operator-install.sh
├── test-cpu-checkpoint.sh
├── test-cpu-restore.sh
├── test-gpu-checkpoint.sh
└── test-gpu-restore.sh
```

## Definition of Done

- [ ] CRDs installiert und validiert
- [ ] Checkpoint Controller funktioniert für CPU-Workloads
- [ ] Checkpoint Controller funktioniert für GPU-Workloads
- [ ] Restore Controller funktioniert für CPU-Workloads
- [ ] Restore Controller funktioniert für GPU-Workloads
- [ ] Counter-Test beweist State-Erhalt (Counter läuft weiter)
- [ ] E2E Tests grün
- [ ] Dokumentation aktualisiert

## Risiken

| Risiko | Mitigation |
|--------|------------|
| containerd API-Änderungen | Pinned containerd-client Version |
| CRIU CUDA-Plugin Inkompatibilität | Version-Matrix dokumentieren |
| Namespace-Isolation | DaemonSet mit hostPID für CRIU-Zugriff |

## Referenzen

- [Design: 07_OPERATOR_DESIGN.md](../design/07_OPERATOR_DESIGN.md)
- [containerd Client Docs](https://pkg.go.dev/github.com/containerd/containerd)
- [Kubebuilder Book](https://book.kubebuilder.io/)
