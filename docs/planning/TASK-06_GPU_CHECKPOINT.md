# Task 06: GPU Checkpoint Implementation

**Status**: 🔄 Phase 2 In Progress (Shim-Integration)
**Phase**: 2 (Integration)
**Letzte Aktualisierung**: 2025-12-04

## Two-Stage GPU Checkpoint Architektur

GPU-Checkpointing erfordert eine **zweistufige Strategie**, da CRIU nicht direkt auf VRAM zugreifen kann:

```
┌─────────────────────────────────────────────────────────────────────┐
│                     KYBERNATE GPU CHECKPOINT                        │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Stage 1: CUDA Checkpoint (VRAM → Host RAM)                        │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  1. cuCheckpointProcessLock(pid)      → Block CUDA calls    │   │
│  │  2. cuCheckpointProcessCheckpoint(pid) → VRAM → RAM copy    │   │
│  │     (GPU memory now in host RAM, process paused)            │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              ↓                                      │
│  Stage 2: CRIU Checkpoint (Host RAM → Disk)                        │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  3. runc checkpoint / criu dump                              │   │
│  │     - Dumps process memory (incl. GPU data in RAM)           │   │
│  │     - Creates pages-*.img files (2+ GB for GPU workloads)    │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              ↓                                      │
│  [Container can be killed / migrated / restored later]             │
│                              ↓                                      │
│  Stage 3: CRIU Restore (Disk → Host RAM)                           │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  4. runc restore / criu restore                              │   │
│  │     - Restores process memory from disk                      │   │
│  │     - GPU data is in host RAM, VRAM still empty              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              ↓                                      │
│  Stage 4: CUDA Restore (Host RAM → VRAM)                           │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  5. cuCheckpointProcessRestore(pid)   → RAM → VRAM copy     │   │
│  │  6. cuCheckpointProcessUnlock(pid)    → Resume CUDA calls   │   │
│  │     (Process continues where it left off)                    │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Validierte Tests (2025-12-04)

| Test | Ergebnis | Details |
|------|----------|---------|
| CUDA Lock | ✅ | `cuCheckpointProcessLock` blockiert CUDA-Aufrufe |
| VRAM → RAM | ✅ | VRAM fällt auf 0 MiB, Daten im Host-RAM |
| CRIU Dump | ✅ | 2.3 GiB Checkpoint inkl. GPU-Daten |
| RAM → VRAM | ✅ | `cuCheckpointProcessRestore` stellt VRAM wieder her |
| Prozess läuft weiter | ✅ | Counter zählt nach Restore weiter |

### Go CUDA Bindings

Implementiert in `pkg/cuda/checkpoint.go`:

```go
// Direct CUDA Driver API calls via cgo
checkpointer, _ := cuda.NewCheckpointer()

// Full checkpoint cycle
checkpointer.CheckpointFull(pid, timeoutMs)  // Lock + VRAM→RAM
checkpointer.RestoreFull(pid)                 // RAM→VRAM + Unlock

// Individual operations
checkpointer.Lock(pid, timeout)
checkpointer.Checkpoint(pid)
checkpointer.Restore(pid)
checkpointer.Unlock(pid)
checkpointer.GetState(pid)  // running/locked/checkpointed
```

CLI-Tool: `bin/cuda-ckpt` für manuelle Tests.

---

## Shim-Integration

### Architektur-Übersicht

Der Kybernate-Shim erweitert den Standard containerd-shim um GPU-Checkpoint-Fähigkeiten:

```
┌─────────────────────────────────────────────────────────────────────┐
│                        KYBERNATE SHIM                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  containerd                                                         │
│      ↓                                                              │
│  containerd-shim-kybernate-v1                                       │
│      │                                                              │
│      ├─→ Checkpoint() ─────────────────────────────────────────┐    │
│      │       │                                                 │    │
│      │       ├─ 1. Detect GPU process (nvidia-smi)             │    │
│      │       ├─ 2. CUDA checkpoint (via pkg/cuda)              │    │
│      │       │      cuCheckpointProcessLock()                  │    │
│      │       │      cuCheckpointProcessCheckpoint()            │    │
│      │       ├─ 3. Delegate to runc.Checkpoint()               │    │
│      │       └─ 4. Store checkpoint path for restore           │    │
│      │                                                         │    │
│      └─→ Create() (with restore) ──────────────────────────────┤    │
│              │                                                 │    │
│              ├─ 1. Delegate to runc.Create() with checkpoint   │    │
│              ├─ 2. Find restored GPU process                   │    │
│              ├─ 3. CUDA restore (via pkg/cuda)                 │    │
│              │      cuCheckpointProcessRestore()               │    │
│              │      cuCheckpointProcessUnlock()                │    │
│              └─ 4. Process continues execution                 │    │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Implementierung: `pkg/service/service.go`

```go
// Key additions to the shim:

// 1. GPU detection
func detectGPUProcess(containerID string) (int, bool) {
    // Parse nvidia-smi output to find GPU process PID
    // Returns (pid, hasGPU)
}

// 2. Enhanced Checkpoint
func (s *Service) Checkpoint(ctx context.Context, req *task.CheckpointTaskRequest) {
    // Check for GPU process
    if pid, hasGPU := detectGPUProcess(containerID); hasGPU {
        // Stage 1: CUDA checkpoint
        checkpointer.CheckpointFull(pid, 30000)
    }
    
    // Stage 2: CRIU checkpoint (via runc)
    return s.Shim.Checkpoint(ctx, req)
}

// 3. Enhanced Create (restore)
func (s *Service) Create(ctx context.Context, req *task.CreateTaskRequest) {
    // Delegate to runc (handles CRIU restore)
    resp, err := s.Shim.Create(ctx, req)
    
    // If restoring and has GPU
    if req.Checkpoint != "" {
        if pid, hasGPU := detectGPUProcess(containerID); hasGPU {
            // Stage 4: CUDA restore
            checkpointer.RestoreFull(pid)
        }
    }
    return resp, err
}
```

### GPU-Erkennung

GPU-Prozesse werden über `nvidia-smi` identifiziert:

```bash
nvidia-smi --query-compute-apps=pid,used_memory --format=csv,noheader
# Output: 283403, 2184 MiB
```

Der Shim korreliert PIDs mit Container-Prozessen über `/proc/<pid>/cgroup`.

### Checkpoint-Speicherung

Checkpoints werden in containerd's Content Store gespeichert:

```
/var/snap/microk8s/common/var/lib/containerd/
  io.containerd.content.v1.content/blobs/sha256/<hash>
```

Für Restore wird der Checkpoint-Pfad via:
1. Annotation: `kybernate.io/restore-from`
2. Environment: `RESTORE_FROM=/path/to/checkpoint`

---

## Validierte Tests (2025-12-04)

### CUDA Checkpoint/Restore Zyklus ✅

```bash
# 1. GPU Pod starten (nvidia RuntimeClass)
microk8s kubectl apply -f manifests/gpu-ckpt-test.yaml

# 2. GPU-Prozess identifizieren
nvidia-smi --query-compute-apps=pid,used_memory --format=csv
# Output: 342458, 2184 MiB

# 3. CUDA Checkpoint (VRAM → RAM)
sudo ./bin/cuda-ckpt --action full-checkpoint --pid 342458 --timeout 30000
# Output: Full checkpoint complete - VRAM is now in host RAM
# nvidia-smi: 0 MiB (VRAM freigegeben)

# 4. CUDA Restore (RAM → VRAM)
sudo ./bin/cuda-ckpt --action full-restore --pid 342458
# Output: Full restore complete - process is running
# nvidia-smi: 2184 MiB (VRAM wiederhergestellt)

# 5. Pod läuft weiter (Loop 4 → 8, keine Unterbrechung)
```

### Shim GPU-Integration ✅

Der Shim wurde um folgende Komponenten erweitert:

| Datei | Funktion |
|-------|----------|
| `shim/pkg/cuda/checkpoint.go` | CUDA Driver API Bindings (cgo) |
| `shim/pkg/cuda/detect.go` | GPU-Prozess-Erkennung via nvidia-smi |
| `shim/pkg/service/service.go` | Integration in Checkpoint/Create Methoden |

**Build und Installation:**
```bash
cd shim
go build -o ../bin/containerd-shim-kybernate-v1 ./cmd/containerd-shim-kybernate-v1/
sudo cp ../bin/containerd-shim-kybernate-v1 /var/snap/microk8s/common/
```

### Bekannte Einschränkungen

1. **RuntimeClass-Konflikt**: Der `kybernate-gpu` RuntimeClass funktioniert nicht direkt, da der Shim die nvidia-container-runtime nicht korrekt einbindet
2. **Workaround**: GPU-Pods mit `nvidia` RuntimeClass deployen, Checkpoint manuell via CLI

### Nächste Schritte

- [ ] Shim um nvidia-container-runtime Proxy erweitern
- [ ] Vollständiger Container Checkpoint/Restore Test (CRIU + CUDA)
- [ ] Kubernetes-Integration für automatische Checkpoint-Trigger

---

## Ziel
Erweiterung des `shim-kybernate-v1` Shims um die Unterstützung für GPU-beschleunigte Container (CUDA). Dies baut auf der erfolgreichen CPU-Checkpoint-Implementierung (Task 05) auf.

## Kontext
In Task 05 haben wir bewiesen, dass der Shim CPU-Workloads checkpointen und wiederherstellen kann. In Task 03 haben wir manuell (ohne Shim) gezeigt, dass `criu` mit dem `criu-cuda-plugin` GPU-Workloads sichern kann. Jetzt müssen wir diese beiden Welten vereinen: Der Shim muss sicherstellen, dass `runc` (und damit `criu`) die korrekten Parameter und Plugins für GPU-Checkpoints verwendet.

## Schritte

### 1. Test Workload Erstellung

Das Image aus Task-03 kann wiederverwendet werden:

```bash
cd phases/phase1/task03-k8s-gpu-checkpoint/workspace

# Image bauen
sudo docker build -t localhost:32000/gpu-pytorch:v1 .

# In MicroK8s Registry pushen (Option A: Push)
sudo docker push localhost:32000/gpu-pytorch:v1

# Alternativ (Option B: Import via tar)
sudo docker save localhost:32000/gpu-pytorch:v1 -o gpu-pytorch-v1.tar
microk8s ctr image import gpu-pytorch-v1.tar
rm gpu-pytorch-v1.tar

# Verifizieren (Option A: Registry API)
curl -s http://localhost:32000/v2/gpu-pytorch/tags/list
# Erwartete Ausgabe: {"name":"gpu-pytorch","tags":["v1"]}

# Verifizieren (Option B: containerd)
microk8s ctr images ls | grep gpu-pytorch
```

Das Script `stress_gpu.py` alloziert ~2GB VRAM und zählt in einer Schleife hoch. Bei erfolgreichem Restore muss der Counter **weiterlaufen** (nicht bei 0 starten).

### 2. Shim Anpassung

#### 2.1 RuntimeClass-Strategie

**Problem**: Kubernetes erlaubt nur **eine** `runtimeClassName` pro Pod. Wir brauchen aber:
- `nvidia` für GPU-Device-Injection (nvidia-container-runtime)
- `kybernate` für Checkpoint/Restore

**Lösung**: Der Kybernate-Shim wird zum **Smart Proxy**, der die passende OCI-Runtime des Clusters nutzt:

```
Pod (runtimeClassName: kybernate)
  └─> containerd-shim-kybernate-v1
        └─> Auto-Detect Runtime:
              ├─> nvidia-container-runtime (wenn GPU vorhanden)
              └─> runc (Fallback für CPU-only)
```

**Implementierung - Runtime-Auswahl-Logik**:

```go
func getRuntimeBinary() string {
    // 1. Explizite Konfiguration via Environment
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
    
    // 3. Fallback: runc
    return "runc"
}
```

**Voraussetzungen für GPU-Support**:

Kybernate setzt voraus, dass der Cluster bereits GPU-fähig konfiguriert ist:
1. NVIDIA GPU Operator ODER manuell installierter `nvidia-container-toolkit`
2. `nvidia-container-runtime` im PATH
3. containerd mit nvidia-runtime konfiguriert

```bash
# Validierung vor Kybernate-Nutzung mit GPU
nvidia-container-runtime --version  # Muss existieren
kubectl get runtimeclass nvidia     # Empfohlen zur Validierung
```

**Zukünftige Verbesserungen** (siehe `shim/docs/RUNTIME_ARCHITECTURE.md`):
- Operator-basierte Validierung bei Installation
- Helm Chart mit konfigurierbarer Runtime
- Automatische RuntimeClass-Erstellung

#### 2.2 Plugin Injection für CRIU

CRIU benötigt das `cuda_plugin.so` für GPU-State. Der Plugin-Pfad:

```bash
# Plugin-Location (verifiziert auf diesem System)
/usr/local/lib/criu/cuda_plugin.so

# Auch vorhanden: AMD GPU Plugin
/usr/local/lib/criu/amdgpu_plugin.so
```

**Im Shim muss beim Checkpoint**:
```go
// Pseudo-Code für Checkpoint-Erweiterung
criuOpts := []string{
    "--lib", "/usr/local/lib/criu",  // Plugin-Verzeichnis (CRIU lädt alle *.so automatisch)
    "--ext-mount-map", "auto",       // NVIDIA-Mounts automatisch behandeln
}
```

**Hinweis**: CRIU lädt alle Plugins aus dem `--lib` Verzeichnis automatisch. Der Plugin-Name `cuda_plugin.so` (nicht `criu-cuda-plugin.so`) ist korrekt.

#### 2.3 Mount Handling

NVIDIA injiziert mehrere Mounts in Container:
- `/usr/local/nvidia/...` (Treiber-Bibliotheken)
- `/dev/nvidia*` (GPU-Devices)

Diese müssen beim Checkpoint korrekt behandelt werden:
- `--ext-mount-map auto` für automatische Mount-Behandlung
- Alternativ: Explizites Excluden problematischer Mounts

#### 2.4 Erforderliche Änderungen in `shim/pkg/service/service.go`

```go
// In der Checkpoint-Methode:
func (s *Service) Checkpoint(ctx context.Context, req *task.CheckpointTaskRequest) (*emptypb.Empty, error) {
    // 1. GPU-Container erkennen (via Annotations oder Device-Mounts)
    isGPU := detectGPUContainer(req)
    
    if isGPU {
        // 2. CRIU-Plugin-Pfad setzen
        os.Setenv("CRIU_LIBS", "/usr/local/lib/criu")
        
        // 3. Checkpoint mit GPU-spezifischen Optionen
        // ... (runc checkpoint --external ...)
    }
    
    return s.Shim.Checkpoint(ctx, req)
}
```

### 3. Verifikation

#### 3.1 Voraussetzung: GPU mit nvidia RuntimeClass testen

Bevor der Kybernate-Shim für GPU angepasst wird, verifizieren dass GPU-Pods grundsätzlich funktionieren:

```bash
# GPU-Pod mit nvidia RuntimeClass (ohne Kybernate)
microk8s kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-nvidia
  namespace: kybernate-system
spec:
  runtimeClassName: nvidia
  containers:
  - name: pytorch
    image: localhost:32000/gpu-pytorch:v1
    resources:
      limits:
        nvidia.com/gpu: 1
    volumeMounts:
    - name: scripts
      mountPath: /workspace/scripts
  volumes:
  - name: scripts
    hostPath:
      path: /home/andre/Workspace/kybernate-evaluation/phases/phase1/task03-k8s-gpu-checkpoint/workspace/scripts
      type: Directory
EOF

# Warten und Logs prüfen
microk8s kubectl wait --for=condition=Ready pod/gpu-test-nvidia -n kybernate-system --timeout=60s
microk8s kubectl logs gpu-test-nvidia -n kybernate-system --tail=10
# Erwartete Ausgabe: "Loop 0: Wert=2.0, VRAM=1908.00 MB", etc.

# VRAM-Belegung prüfen
nvidia-smi | grep python
# Erwartete Ausgabe: ~2000MiB belegt

# Aufräumen
microk8s kubectl delete pod gpu-test-nvidia -n kybernate-system
```

**Hinweis**: Mit `runtimeClassName: kybernate` funktioniert GPU-Zugriff zunächst NICHT, da der Shim `runc` statt `nvidia-container-runtime` verwendet. Dies wird in Schritt 2.1 (RuntimeClass-Strategie) behoben.

#### 3.2 GPU-Pod mit Kybernate RuntimeClass

Nach der Shim-Anpassung:

```bash
# 1. GPU-Pod deployen
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-stress-test
  namespace: kybernate-system
spec:
  runtimeClassName: kybernate
  containers:
  - name: pytorch
    image: localhost:32000/gpu-pytorch:v1
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# 2. Warten bis Pod läuft und Counter sichtbar
kubectl logs -f gpu-stress-test -n kybernate-system
# Erwartete Ausgabe: "Iteration 1...", "Iteration 2...", etc.

# 3. Checkpoint erstellen (bei z.B. Iteration 50)
CONTAINER_ID=$(microk8s ctr -n k8s.io c ls | grep gpu-stress | awk '{print $1}')
sudo microk8s ctr -n k8s.io task checkpoint $CONTAINER_ID --checkpoint-path /tmp/gpu-checkpoint

# 4. Pod löschen
kubectl delete pod gpu-stress-test -n kybernate-system

# 5. Restore-Pod starten
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-stress-restored
  namespace: kybernate-system
spec:
  runtimeClassName: kybernate
  containers:
  - name: pytorch
    image: localhost:32000/gpu-pytorch:v1
    env:
    - name: RESTORE_FROM
      value: "/tmp/gpu-checkpoint"
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# 6. Logs prüfen - Counter muss bei ~50 weiterlaufen!
kubectl logs -f gpu-stress-restored -n kybernate-system
# Erwartete Ausgabe: "Iteration 51...", "Iteration 52...", etc.

# 7. VRAM-Belegung prüfen
nvidia-smi
# Erwartete Ausgabe: ~2GB VRAM belegt
```

## Technische Herausforderungen

### Plugin-Pfad
CRIU lädt Plugins aus einem konfigurierbaren Verzeichnis. Das `cuda_plugin.so` liegt unter `/usr/local/lib/criu/`. Der Shim muss dies via `--lib /usr/local/lib/criu` an runc/CRIU übergeben.

### RuntimeClass-Konflikt
Kubernetes erlaubt nur eine RuntimeClass pro Pod. Die Lösung ist ein **Wrapper-Ansatz**: `kybernate-shim` ruft intern `nvidia-container-runtime` auf, das wiederum `runc` startet.

### Privilegien
GPU-Zugriff und Checkpoint erfordern hohe Privilegien:
- Container muss `privileged: true` oder spezifische Capabilities haben
- Der Shim läuft bereits als root

### Mount-Excludes
NVIDIA injiziert dynamische Mounts, die beim Checkpoint problematisch sein können:
- `/proc/{pid}/mountinfo` Einträge
- Lösung: `--ext-mount-map auto` oder explizite `--external` Flags

### Checkpoint-Größe
Bei ~2GB VRAM-Allokation ist mit einem Checkpoint von **2-3GB** zu rechnen (VRAM-Dump + CPU-State). Dies beeinflusst:
- Checkpoint-Dauer (~5-15 Sekunden je nach NVMe-Speed)
- Speicherplatzbedarf
- Restore-Dauer

**Verifizierte Messungen (2025-12-03)**:

| Typ | Größe | Beschreibung |
|-----|-------|--------------|
| Container-Image | 3.2 GiB | PyTorch + CUDA Libraries |
| GPU-Checkpoint (2GB VRAM) | 2.2 GiB | Prozess-State + VRAM-Dump |
| CPU-only Checkpoint | ~4 MiB | Nur Prozess-State |

**Wichtig**: Der Checkpoint enthält **nur den Prozess-State**, nicht das Container-Filesystem. Das Filesystem kommt vom originalen Container-Image bei Restore.

## Verifizierter GPU-Checkpoint Workflow

### Voraussetzungen

```bash
# 1. CRIU CUDA Plugin vorhanden
ls -la /usr/local/lib/criu/cuda_plugin.so

# 2. nvidia RuntimeClass verfügbar
microk8s kubectl get runtimeclass nvidia

# 3. GPU-Image in Registry
curl -s http://localhost:32000/v2/gpu-pytorch/tags/list
```

### Schritt 1: GPU-Pod mit 2GB VRAM starten

```bash
# Pod mit nvidia RuntimeClass und stress_gpu.py Script
microk8s kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test
  namespace: kybernate-system
spec:
  runtimeClassName: nvidia
  containers:
  - name: pytorch
    image: localhost:32000/gpu-pytorch:v1
    command: ["python", "/workspace/scripts/stress_gpu.py"]
    resources:
      limits:
        nvidia.com/gpu: 1
    volumeMounts:
    - name: scripts
      mountPath: /workspace/scripts
  volumes:
  - name: scripts
    hostPath:
      path: /home/andre/Workspace/kybernate-evaluation/phases/phase1/task03-k8s-gpu-checkpoint/workspace/scripts
      type: Directory
EOF

# Warten und Status prüfen
sleep 15
microk8s kubectl logs gpu-test -n kybernate-system --tail=10
```

**Erwartete Ausgabe:**
```
[2025-12-03T11:36:46.502467] Starte GPU Stress Runner (PID=1)
[2025-12-03T11:36:46.630423] Nutze Device: Quadro RTX 5000 with Max-Q Design
[2025-12-03T11:36:46.630447] Alloziere Tensor mit 500000000 Elementen (~2GB)
[2025-12-03T11:36:46.766978] Allokation erfolgreich
[2025-12-03T11:36:46.783596] Loop 0: Wert=2.0, VRAM=1908.00 MB
[2025-12-03T11:36:51.799963] Loop 5: Wert=7.0, VRAM=1908.00 MB
```

### Schritt 2: VRAM-Belegung verifizieren

```bash
# nvidia-smi zeigt ~2GB VRAM
nvidia-smi

# Oder in nvtop beobachten
nvtop
```

**Erwartete Ausgabe:**
- Python-Prozess mit ~1900-2000 MiB VRAM

### Schritt 3: GPU-Checkpoint erstellen

```bash
# Container-ID ermitteln
CONTAINER_ID=$(sudo /snap/microk8s/current/bin/ctr \
    --namespace k8s.io \
    --address /var/snap/microk8s/common/run/containerd.sock \
    containers list 2>/dev/null | grep pytorch | awk '{print $1}')

echo "Container: $CONTAINER_ID"

# Checkpoint mit CRIU CUDA Plugin
sudo CRIU_LIBS=/usr/local/lib/criu /snap/microk8s/current/bin/ctr \
    --namespace k8s.io \
    --address /var/snap/microk8s/common/run/containerd.sock \
    tasks checkpoint "$CONTAINER_ID" \
    --checkpoint-path /tmp/gpu-checkpoint
```

**Erwartete Ausgabe:**
```
containerd.io/checkpoint/88caaacf...:12-03-2025-12:37:35
```

### Schritt 4: Checkpoint verifizieren

```bash
# Checkpoint-Image prüfen
sudo /snap/microk8s/current/bin/ctr \
    --namespace k8s.io \
    --address /var/snap/microk8s/common/run/containerd.sock \
    images ls 2>/dev/null | grep checkpoint | grep "$CONTAINER_ID"
```

**Erwartete Ausgabe:**
```
containerd.io/checkpoint/88caaacf...:12-03-2025-12:37:35  ...  2.2 GiB  linux/amd64  containerd.io/checkpoint=true
```

### Schritt 5: Pod läuft nach Checkpoint weiter

```bash
# Counter zählt weiter (kein Kill durch Checkpoint)
microk8s kubectl logs gpu-test -n kybernate-system --tail=5
```

**Erwartete Ausgabe:**
```
[2025-12-03T11:37:52.470997] Loop 50: Wert=52.0, VRAM=1908.00 MB
[2025-12-03T11:37:57.486983] Loop 55: Wert=57.0, VRAM=1908.00 MB
...
```

### Automatisiertes Script

Alternativ kann das Checkpoint-Script verwendet werden:

```bash
./phases/phase1/task03-k8s-gpu-checkpoint/scripts/gpu-checkpoint.sh gpu-test
```

## GPU-Restore Analyse

### Checkpoint-Struktur

Der GPU-Checkpoint ist ein POSIX tar-Archiv im containerd Content-Store:

```
/var/snap/microk8s/common/var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/<sha>
```

**Inhalt des Checkpoints (54 Dateien):**

| Datei | Größe | Beschreibung |
|-------|-------|--------------|
| `pages-7.img` | 2.3 GiB | GPU-Speicher (CUDA) |
| `pages-1..6.img` | je 2 MB | CPU-Speicher Pages |
| `core-*.img` | < 3 KB | Thread-States |
| `mm-1.img` | 21 KB | Memory-Map Descriptors |
| `pagemap-*.img` | < 30 KB | Page-Mapping Metadaten |
| `tmpfs-*.tar.gz.img` | variabel | tmpfs Mounts |
| weitere | < 20 KB | Namespaces, Pipes, etc. |

### Restore-Optionen

#### Option A: containerd API (nicht implementiert)

```bash
# CRI CheckpointContainer nicht implementiert in MicroK8s
crictl checkpoint --export=/tmp/cp.tar <container>
# Fehler: method CheckpointContainer not implemented
```

#### Option B: runc restore (manuell)

Erfordert:
1. OCI-Bundle mit rootfs (vom Original-Image)
2. config.json (Container-Konfiguration)
3. Checkpoint-Verzeichnis mit CRIU-Images

```bash
# Voraussetzung: Bundle und Checkpoint extrahiert
sudo CRIU_LIBS=/usr/local/lib/criu runc restore \
    --bundle /path/to/bundle \
    --image-path /tmp/gpu-restore/checkpoint \
    --work /tmp/runc-work \
    <container-id>
```

**Problem**: Erfordert manuelles Bundle-Setup, nicht Kubernetes-integriert.

#### Option C: containerd Restore (experimentell)

```bash
# Theorie: ctr tasks start mit Checkpoint
sudo /snap/microk8s/current/bin/ctr \
    --namespace k8s.io \
    --address /var/snap/microk8s/common/run/containerd.sock \
    tasks start <container-id> --checkpoint <checkpoint-image>
```

**Status**: Noch nicht getestet für GPU-Workloads.

### Bekannte Einschränkungen

1. **Kubernetes-Integration fehlt**: K8s CRI unterstützt `CheckpointContainer` nicht in MicroK8s
2. **nvidia-container-runtime**: Muss beim Restore die gleichen GPU-Devices injizieren
3. **CUDA Context**: GPU muss beim Restore verfügbar sein (gleiche oder kompatible GPU)

### Empfohlener Workflow für Production

Für Task 06 (Phase 1) ist der **manuelle Checkpoint/Restore** Workflow ausreichend:

1. **Checkpoint**: Über `ctr tasks checkpoint` (funktioniert ✓)
2. **Restore**: Über separaten Operator/Controller außerhalb von K8s
3. **Zukünftige Integration**: Custom CRI-Plugin oder Shim-Erweiterung

## Offene Fragen

- [x] ~~Wie interagiert `nvidia-container-runtime` mit dem Checkpoint-Prozess?~~
  → Checkpoint funktioniert mit nvidia RuntimeClass, CRIU CUDA Plugin wird korrekt geladen
- [ ] Müssen CUDA-Kontexte vor dem Checkpoint in einen bestimmten Zustand gebracht werden?
- [ ] Funktioniert Restore auf einer anderen GPU (gleiches Modell)?
- [x] ~~Wie wird der Restore-Workflow implementiert (Kubernetes CRI vs. manuell)?~~
  → CRI nicht implementiert in MicroK8s, manueller Restore via runc/ctr erforderlich

## Definition of Done

- [x] Test-Image `gpu-pytorch:v1` ist gebaut und in MicroK8s Registry verfügbar
- [x] GPU-Pod läuft mit nvidia RuntimeClass und reserviert ~2GB VRAM
- [x] GPU-Checkpoint kann erstellt werden (via `ctr tasks checkpoint`)
- [x] Checkpoint-Größe zeigt VRAM-Erfassung (2.2 GiB statt 4 MiB)
- [x] Pod läuft nach Checkpoint weiter (kein Kill)
- [x] Checkpoint-Struktur analysiert (CRIU-Images inkl. pages-7.img mit GPU-Speicher)
- [ ] Shim erkennt GPU-Container (via Device-Requests oder Annotations) - *Deferred: nvidia RuntimeClass*
- [ ] GPU-Restore via runc/ctr getestet - *Blocked: Manuelle Bundle-Erstellung erforderlich*
- [ ] Applikations-Log beweist State-Erhalt (Counter läuft weiter, nicht Neustart)
- [ ] `nvidia-smi` zeigt nach Restore die erwartete VRAM-Belegung

## Referenzen

- [CRIU CUDA Plugin](https://github.com/checkpoint-restore/criu/tree/master/plugins/cuda)
- [nvidia-container-runtime](https://github.com/NVIDIA/nvidia-container-runtime)
- Task-03: Manueller GPU-Checkpoint (ohne Shim)
- Task-05: CPU-Checkpoint im Shim (Basis für GPU-Erweiterung)
