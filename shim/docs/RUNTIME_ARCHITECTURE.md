# Kybernate Runtime Architecture

Dieses Dokument beschreibt die Architektur der Runtime-Integration im Kybernate Shim.

## Status Quo und Herausforderungen

### Das Problem

Der Kybernate Shim (`containerd-shim-kybernate-v1`) basiert auf dem containerd runc-v2 Shim.
Die Integration mit `nvidia-container-runtime` ist **nicht trivial**, weil:

1. **containerd's Runtime-Modell**: containerd unterscheidet zwischen:
   - **Shim** (`io.containerd.runc.v2`): Prozess-Manager, Lifecycle-Handler
   - **OCI Runtime** (`runc`, `nvidia-container-runtime`): Führt Container aus
   
2. **BinaryName funktioniert nur mit runc-v2 Shim**: Die containerd-Option `BinaryName` wird
   nur vom Standard `containerd-shim-runc-v2` interpretiert. Custom Shims erben diese Option nicht.

3. **runc Package Limitierung**: Das `containerd/runtime/v2/runc/v2` Go-Package, das unser Shim
   nutzt, verwendet `RUNC_BINARY` Environment Variable, aber diese wird nicht konsistent
   an Subprozesse weitergegeben.

### Aktuelle Lösung (Phase 1)

Für GPU-Workloads mit Checkpoint/Restore verwenden wir einen **hybriden Ansatz**:

```
┌─────────────────────────────────────────────────────────────┐
│                 GPU Workload mit Checkpoint                 │
└─────────────────────────────────────────────────────────────┘

Container-Erstellung:
┌─────────────┐    ┌─────────────────┐    ┌──────────────────┐
│   Pod mit   │───►│ nvidia Runtime  │───►│ Container läuft  │
│   nvidia    │    │ Class           │    │ mit GPU-Zugriff  │
│ RuntimeClass│    │                 │    │                  │
└─────────────┘    └─────────────────┘    └──────────────────┘

Checkpoint/Restore:
┌─────────────────┐    ┌─────────────────┐    ┌───────────────┐
│  ctr/crictl     │───►│ CRIU mit        │───►│  Checkpoint   │
│  checkpoint     │    │ cuda_plugin.so  │    │  gespeichert  │
└─────────────────┘    └─────────────────┘    └───────────────┘
```

**Vorteile:**
- Nutzt bewährte nvidia RuntimeClass
- CRIU GPU-Plugin funktioniert unabhängig vom Shim
- Keine Kernel/containerd Modifikationen nötig

**Nachteile:**
- Kein einheitlicher Workflow für CPU und GPU
- Checkpoint muss manuell via `ctr` oder `crictl` ausgelöst werden

### CPU-only Workloads

Für CPU-Workloads funktioniert der Kybernate Shim direkt:

```
┌─────────────────────────────────────────────────────────────┐
│                     Kubernetes Pod                          │
│                 runtimeClassName: kybernate                 │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                       containerd                            │
│            runtime_type: io.containerd.kybernate.v1         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│              containerd-shim-kybernate-v1                   │
│                           │                                 │
│                           ▼                                 │
│              ┌──────────────────────┐                       │
│              │        runc          │                       │
│              │   (CPU Container)    │                       │
│              └──────────────────────┘                       │
└─────────────────────────────────────────────────────────────┘
```

## Aktuelle Implementierung (Phase 1)

### Runtime-Auswahl

Die Runtime wird beim Shim-Start einmalig ermittelt:

```go
func getRuntimeBinary() string {
    // 1. Explizite Konfiguration
    if rt := os.Getenv("KYBERNATE_RUNTIME"); rt != "" {
        return rt
    }
    
    // 2. Auto-Detect nvidia-container-runtime
    candidates := []string{
        "nvidia-container-runtime",
        "/usr/bin/nvidia-container-runtime", 
        "/usr/local/nvidia/toolkit/nvidia-container-runtime",
    }
    for _, c := range candidates {
        if _, err := exec.LookPath(c); err == nil {
            return c
        }
    }
    
    // 3. Fallback
    return "runc"
}
```

### Konfiguration

**Option 1: Automatische Erkennung (Standard)**

Keine Konfiguration nötig. Der Shim erkennt `nvidia-container-runtime` automatisch.

**Option 2: Explizite Konfiguration**

```bash
# Via Environment Variable
export KYBERNATE_RUNTIME=/usr/bin/nvidia-container-runtime
```

Oder in der containerd-Konfiguration:

```toml
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate]
  runtime_type = "io.containerd.kybernate.v1"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate.options]
    # Wird als KYBERNATE_RUNTIME an den Shim übergeben
    SystemdCgroup = false
```

## Voraussetzungen

### Für CPU-only Workloads
- `runc` installiert (Standard bei allen Kubernetes-Distributionen)

### Für GPU Workloads
1. **NVIDIA Treiber** installiert
2. **nvidia-container-toolkit** installiert:
   ```bash
   # Ubuntu/Debian
   distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
   curl -s -L https://nvidia.github.io/nvidia-container-runtime/gpgkey | sudo apt-key add -
   curl -s -L https://nvidia.github.io/nvidia-container-runtime/$distribution/nvidia-container-runtime.list | \
     sudo tee /etc/apt/sources.list.d/nvidia-container-runtime.list
   sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
   ```
3. **containerd konfiguriert** für nvidia-runtime
4. **CRIU CUDA Plugin** für Checkpoint:
   ```bash
   ls /usr/local/lib/criu/cuda_plugin.so
   ```
5. **nvidia RuntimeClass** vorhanden (automatisch bei MicroK8s nvidia addon)

### Validierung

```bash
# Prüfe nvidia-container-runtime
nvidia-container-runtime --version

# Prüfe CRIU CUDA Plugin
ls -la /usr/local/lib/criu/cuda_plugin.so

# Prüfe GPU-Zugriff in einem Container
docker run --rm --gpus all nvidia/cuda:12.0-base nvidia-smi

# Prüfe nvidia RuntimeClass
kubectl get runtimeclass nvidia
```

## Zukünftige Verbesserungen

### Phase 2: Native GPU-Shim Integration

**Option A: Fork containerd-shim-runc-v2**

Direkte Modifikation des runc-v2 Shims um `BinaryName` Option zu respektieren:

```go
// In shim initialization
func New(ctx context.Context, id string, ...) {
    binaryName := "runc"
    if bn := os.Getenv("CONTAINERD_SHIM_BINARY_NAME"); bn != "" {
        binaryName = bn
    }
    // Use binaryName for all runc calls
}
```

**Option B: Wrapper-Shim mit direkter Runtime-Invokation**

Statt das runc-Package zu nutzen, die OCI-Runtime direkt aufrufen:

```go
func (s *Service) Create(ctx context.Context, req *task.CreateTaskRequest) {
    runtime := s.selectRuntime(req.Bundle)
    
    // Direkte OCI runtime Invokation
    cmd := exec.Command(runtime, "create", "--bundle", req.Bundle, req.ID)
    cmd.Run()
}

func (s *Service) selectRuntime(bundle string) string {
    if s.hasGPUResources(bundle) {
        return "nvidia-container-runtime"
    }
    return "runc"
}
```

**Vorteile**: Einheitlicher Workflow für CPU und GPU
**Nachteile**: Mehr Code zu maintainen, OCI-Spec Compliance sicherstellen

### Phase 3: Operator-Integration

Ein Kybernate Operator könnte:

1. **Voraussetzungen validieren** bei Installation
2. **RuntimeClass automatisch erstellen** mit korrektem Handler
3. **Node Labels setzen** basierend auf GPU-Verfügbarkeit

### Phase 4: CRI-Level Integration

Checkpoint/Restore über Kubernetes CRI statt direktem containerd-Zugriff:

```bash
# Kubernetes 1.25+ mit ContainerCheckpoint Feature Gate
kubectl checkpoint pod/gpu-test -c pytorch --checkpoint-path=/tmp/checkpoint
```

Dies würde die Integration in Kubernetes-Native Workflows ermöglichen.

## Debugging

### Shim-Log

```bash
# Runtime-Auswahl wird geloggt
sudo cat /tmp/kybernate-shim.log
```

### GPU Container Test

```bash
# Test mit nvidia RuntimeClass
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test
spec:
  runtimeClassName: nvidia
  containers:
  - name: test
    image: nvidia/cuda:12.0-base
    command: ["nvidia-smi"]
  restartPolicy: Never
EOF

# Prüfe GPU-Zugriff
kubectl logs gpu-test
```

## Referenzen

- [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/)
- [containerd Runtime Configuration](https://github.com/containerd/containerd/blob/main/docs/cri/config.md)
- [OCI Runtime Specification](https://github.com/opencontainers/runtime-spec)
- [CRIU CUDA Support](https://criu.org/CUDA)
