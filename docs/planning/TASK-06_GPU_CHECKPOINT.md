# Task 06: GPU Checkpoint Implementation

**Status**: Pending
**Phase**: 1 (Foundation)

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

# Verifizieren
microk8s ctr images ls | grep gpu-pytorch
```

Das Script `stress_gpu.py` alloziert ~2GB VRAM und zählt in einer Schleife hoch. Bei erfolgreichem Restore muss der Counter **weiterlaufen** (nicht bei 0 starten).

### 2. Shim Anpassung

#### 2.1 RuntimeClass-Strategie

**Problem**: Kubernetes erlaubt nur **eine** `runtimeClassName` pro Pod. Wir brauchen aber:
- `nvidia` für GPU-Device-Injection (nvidia-container-runtime)
- `kybernate` für Checkpoint/Restore

**Lösung**: Der Kybernate-Shim wird zum **Wrapper** für nvidia-container-runtime:

```
Pod (runtimeClassName: kybernate)
  └─> containerd-shim-kybernate-v1
        └─> nvidia-container-runtime (statt runc)
              └─> runc
```

**Umsetzung in `service.go`**:
- Statt `runc` direkt aufzurufen, `nvidia-container-runtime` verwenden
- Alternativ: Environment-Variable `NVIDIA_VISIBLE_DEVICES` manuell setzen

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

## Offene Fragen

- [ ] Wie interagiert `nvidia-container-runtime` mit dem Checkpoint-Prozess?
- [ ] Müssen CUDA-Kontexte vor dem Checkpoint in einen bestimmten Zustand gebracht werden?
- [ ] Funktioniert Restore auf einer anderen GPU (gleiches Modell)?

## Definition of Done

- [ ] Test-Image `gpu-pytorch:v1` ist gebaut und in MicroK8s Registry verfügbar
- [ ] Shim erkennt GPU-Container (via Device-Requests oder Annotations)
- [ ] Shim lädt `cuda_plugin.so` beim Checkpoint (via `--lib /usr/local/lib/criu`)
- [ ] Shim kann GPU-Container checkpointen (Files werden erstellt, inkl. VRAM-Dump)
- [ ] Shim kann GPU-Container restoren (GPU wird wieder alloziert)
- [ ] Applikations-Log beweist State-Erhalt (Counter läuft weiter, nicht Neustart)
- [ ] `nvidia-smi` zeigt nach Restore die erwartete VRAM-Belegung

## Referenzen

- [CRIU CUDA Plugin](https://github.com/checkpoint-restore/criu/tree/master/plugins/cuda)
- [nvidia-container-runtime](https://github.com/NVIDIA/nvidia-container-runtime)
- Task-03: Manueller GPU-Checkpoint (ohne Shim)
- Task-05: CPU-Checkpoint im Shim (Basis für GPU-Erweiterung)
