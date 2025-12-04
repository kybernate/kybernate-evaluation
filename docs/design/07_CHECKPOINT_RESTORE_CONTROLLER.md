## Option C: Neuen Pod erstellen + Process State injizieren

#### Konzept

Die im Projekt aktuell verfolgte und bereits teilweise implementierte Strategie entspricht Option C:

 - **Kubernetes-Ebene**: Es wird ein neuer Pod erstellt (gleiches Image, gleiche Ressourcen, gleiche Volumes), der anstatt „from scratch“ zu starten, den vorher erstellten Checkpoint als Eingabe nutzt.
 - **Runtime-Ebene**: Die Runtime (hier `kybernate-runtime` als `RuntimeClass`) erkennt anhand von Annotationen/Env-Variablen, dass dieser Pod/Container aus einem Checkpoint wiederhergestellt werden soll, und führt einen CRIU-Restore durch.
 - **GPU-Ebene**: Nach dem erfolgreichen CRIU-Restore wird im wiederhergestellten Container der zugehörige GPU-Prozess gefunden und über die CUDA-API aus dem „checkpointed“- in den „running“-Zustand überführt (RAM → VRAM).

Damit „weiß“ Kubernetes weiterhin nur, dass ein neuer Pod läuft – alle Low-Level-Details des Restore-Prozesses sind in der Runtime abstrahiert.

#### High-Level Flow

1. User erstellt einen `KybernateCheckpoint` mit `action: restore` und Verweis auf einen bestehenden Checkpoint.
2. Der Kybernate-Controller liest den Status des ursprünglichen Checkpoints (inkl. `containerInfo`).
3. Der Controller erzeugt einen **neuen Pod** (z.B. `pytorch-training-restored`) mit:
  - `runtimeClassName: kybernate-gpu`
  - Annotation `kybernate.io/restore-from: <checkpointPath>` **oder** Environment-Variable `RESTORE_FROM=<checkpointPath>` im Zielcontainer.
4. Der Pod wird von kubelet/containerd mit `kybernate-runtime` gestartet.
5. `kybernate-runtime` erkennt anhand der Annotation/Env, dass ein Restore gewünscht ist, und führt einen CRIU-Restore mit dem angegebenen Checkpoint-Verzeichnis durch.
6. Nach dem erfolgreichen Container-Restore sucht das Shim nach einem GPU-Prozess im wiederhergestellten Container und ruft die CUDA-Restore-API auf.
7. Der Controller überwacht den Status des neuen Pods und aktualisiert den `KybernateCheckpoint`-Status (phase, message, ggf. neue `containerInfo`).

#### Aktuelle Implementierung im Code

Die für Option C relevanten Teile sind bereits im `shim`-Baum implementiert.

**1. Runtime-Level Restore-Erkennung (`shim/pkg/service/service.go`)**

Die Datei `shim/pkg/service/service.go` implementiert einen Service, der den Standard-runc-shim um GPU-Checkpoint/Restore erweitert. Besonders wichtig ist die `Create`-Methode:

 - Sie lädt bei einem neuen Container-Start die OCI-Spec (`config.json`) aus dem Bundle-Verzeichnis.
 - Sie prüft auf zwei Arten, ob ein Restore gewünscht ist:
  - Annotation: `spec.Annotations["kybernate.io/restore-from"]`
  - Environment-Variable im Container-Prozess: `RESTORE_FROM=<checkpointPath>`
 - Falls ein solcher Hinweis gefunden wird, wird `req.Checkpoint = <checkpointPath>` gesetzt. Das bedeutet, dass der darunterliegende runc-Shim anstelle eines „normalen“ Starts einen **Restore** aus dem angegebenen Checkpoint-Verzeichnis ausführt.
 - Zusätzlich wird sichergestellt, dass für GPU-Workloads `nvidia-container-runtime` verwendet wird (Anpassung von `options.json`).

Nach dem eigentlichen `Create`-Call auf den unterliegenden Shim (also nach dem CRIU-Restore) wird – **falls `isRestore == true` und `cudaCheckpointer != nil`** – folgendes gemacht:

 - Kurzes Warten, bis der Prozess wirklich läuft.
 - Finden eines GPU-Prozesses im Task (`cuda.FindAnyGPUProcessForTask(int(resp.Pid))`).
 - Abfragen des CUDA-Zustands dieses Prozesses (`GetState`).
 - Falls `state == cuda.StateCheckpointed`, wird `RestoreFull(gpuPID)` aufgerufen, d.h. die VRAM-Daten werden aus dem Host-RAM zurück in die GPU geladen.

Damit ist **Option C auf Runtime/GPU-Ebene bereits funktionsfähig**, sofern der Container mit korrekter Annotation/Env und passendem `CheckpointPath` gestartet wird.

**2. Node-seitiger Checkpoint/Restore-Controller (`shim/pkg/checkpoint/controller.go`)**

Die Datei `shim/pkg/checkpoint/controller.go` stellt einen Node-seitigen Controller bereit, der GPU-Checkpoint/Restore über CUDA + Kubernetes Checkpoint API implementiert.

 - `Checkpoint(...)` implementiert den Two-Stage-Checkpoint:
  1. CUDA-Checkpoint (VRAM → RAM) über `cuda.Checkpointer`.
  2. CRIU-Checkpoint über `kubectl checkpoint` bzw. direktes Kubelet-API.

 - `Restore(...)` ist strukturell vorbereitet und beschreibt ebenfalls zwei Stages:
  1. `restoreFromCheckpoint(...)`: Stellt den Container aus einem Checkpoint-Verzeichnis wieder her.
  2. CUDA-Restore: Wenn ein GPU-Prozess gefunden wird und im Zustand `Checkpointed` ist, wird `RestoreFull` aufgerufen.

Aktuell ist `restoreFromCheckpoint(...)` allerdings nur ein **Platzhalter** und gibt `"restore not yet implemented - requires CRI restore API"` zurück. Für Option C ist dieser Platzhalter nicht zwingend erforderlich, weil der eigentliche Restore über einen neuen Pod + `kybernate-runtime` erfolgen kann.

#### Geplanter Kubernetes-Controller-Flow (Control Plane)

Um Option C vollständig umzusetzen, wird ein dedizierter Kubernetes-Controller benötigt, der die oben beschriebenen Schritte orches­triert. Dieser Controller läuft als Deployment und beobachtet `KybernateCheckpoint`-CRs.

Für `action: restore` wäre der Ablauf:

1. **CR lesen**
  - `spec.action == restore`
  - `spec.restore.fromCheckpoint` verweist auf einen bestehenden `KybernateCheckpoint` (z.B. `gpu-workload-checkpoint-000`).

2. **Checkpoint-Metadaten laden**
  - Aus dem referenzierten Checkpoint-Objekt bzw. aus der Metadatendatei (z.B. `kybernate-metadata.json`) das ursprüngliche Container-Image, Pod-UID, `runtimeClass`, Volumes etc. auslesen.

3. **Neuen Pod-Spec erzeugen**
  - Basis: ursprünglicher Pod (`status.containerInfo.podSpec` o.ä.).
  - Anpassungen:
    - Neuer Name (`spec.restore.targetPodName` oder generiert).
    - `runtimeClassName: kybernate-gpu` (falls nicht bereits gesetzt).
    - Annotation `kybernate.io/restore-from: <checkpointPath>` **oder** Env `RESTORE_FROM=<checkpointPath>` im Zielcontainer.
  - Optional: zusätzliche Labels setzen, um „restored from <checkpoint>“ zu kennzeichnen.

4. **Pod erstellen**
  - Über Kubernetes-API (client-go oder `kubectl apply`) wird der neue Pod erzeugt.

5. **Pod-Status überwachen**
  - Der Controller wartet, bis der Pod in Phase `Running` (oder Fehler) ist.
  - Optional: Abfragen von Logs/Events zur Diagnose.

6. **Status des `KybernateCheckpoint` aktualisieren**
  - `status.phase = Completed` oder `Failed`.
  - `status.message` mit Erfolgs-/Fehlerdetails.
  - Optional: neues `containerInfo`-Feld mit `originalCheckpoint`, `restoredPodName`, `restoredContainerID` etc.

#### Vorteile von Option C

 - **Kubernetes-konform**: Kubernetes sieht nur „es wird ein neuer Pod gestartet“. Es werden keine inoffiziellen containerd/CRI-APIs im Control Plane benötigt.
 - **Saubere Trennung**: Checkpoint/Restore-Logik ist in der Runtime (Node-Ebene) gekapselt, der Controller orchestriert nur Ressourcen.
 - **Gut testbar**: Man kann Restore isoliert testen, indem man Pods mit der Restore-Annotation/Env startet, ohne den gesamten Controller zu benötigen.

#### Nachteile / Offene Punkte

 - **Zusätzliche Latenz**: Pod-Neustart dauert länger als ein reines Prozess-Restore über containerd.
 - **Metadaten-Management**: Der Controller muss ausreichend Informationen zum ursprünglichen Pod speichern, um einen sinnvollen Restore-Pod bauen zu können (Volumes, Ressourcen, Env, Argumente, etc.).
 - **Lebenszyklus von Checkpoints**: Für `deleteAfterRestore: true` muss der Controller entscheiden, wann Checkpoints sicher gelöscht werden können.

---

## Zusammenfassung der Optionen

| Option | Beschreibung                                             | Kubernetes-Integration       | Implementierungsstatus (Stand Repo)                    |
|--------|----------------------------------------------------------|------------------------------|--------------------------------------------------------|
| A      | Direkte Nutzung von `containerd.Task.Restore()`         | Keine CRI-Unterstützung      | Nicht implementiert, nur als Design-Variante betrachtet|
| B      | Direktes `runc restore` + manuelle Bundle-Rekonstruktion| Außerhalb von K8s            | Nicht implementiert, als zu komplex bewertet          |
| C      | Neuer Pod + Restore-Annotation → Runtime + CUDA-API     | Voll K8s-kompatibel          | Runtime-seitig (shim, CUDA) implementiert, Controller-Flow geplant |

Für die weitere Entwicklung ist **Option C die bevorzugte und bereits teilweise umgesetzte Strategie**. Die nächsten Schritte bestehen darin, den beschriebenen `KybernateCheckpoint`-Controller (mit CRD, Reconciler und Status-Management) zu implementieren und `restoreFromCheckpoint` klar auf diesen High-Level-Flow auszurichten oder entsprechend zu vereinfachen.

````
# Kybernate Checkpoint/Restore Controller - Design Document

## Übersicht

Dieses Dokument beschreibt die Architektur und Implementierung eines Kubernetes Controllers für GPU-aware Container Checkpoint/Restore. Der Controller ermöglicht das Suspendieren von GPU-Workloads (VRAM + RAM → Disk) und deren spätere Wiederherstellung.

## Kontext und Problemstellung

### Ausgangslage

Wir haben bereits implementiert:
- **CUDA Checkpoint API Bindings** (`shim/pkg/cuda/checkpoint.go`)
  - `cuCheckpointProcessLock()` - Blockiert CUDA API Calls
  - `cuCheckpointProcessCheckpoint()` - Transferiert VRAM → RAM
  - `cuCheckpointProcessRestore()` - Transferiert RAM → VRAM
  - `cuCheckpointProcessUnlock()` - Gibt CUDA API Calls frei

- **kybernate-runtime** (`shim/cmd/kybernate-runtime/`)
  - OCI Runtime Wrapper
  - Interceptiert checkpoint/restore Befehle
  - Führt CUDA Checkpoint vor CRIU aus
  - Delegiert an nvidia-container-runtime

- **kybernate-ctl** (`shim/cmd/kybernate-ctl/`)
  - CLI Tool für manuelles Checkpoint
  - Two-Stage Checkpoint funktioniert (CUDA + CRIU)
  - Erstellt 2.4GB Checkpoint Files

### Das Problem

**Checkpoint funktioniert, aber Restore nicht!**

Der CRIU Checkpoint erstellt erfolgreich Speicherabbilder auf Disk. Das Problem ist die **Wiederherstellung des Containers** in Kubernetes:

1. Kubernetes hat keine native "Restore from Checkpoint" API
2. containerd's Task API hat `Restore()`, aber es ist nicht über CRI exponiert
3. runc kann `restore` ausführen, braucht aber den originalen Bundle-Pfad

## Architektur

### Komponenten

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              KUBERNETES                                      │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                    Kybernate Controller (Deployment)                    │ │
│  │                                                                         │ │
│  │  Responsibilities:                                                      │ │
│  │  - Watches KybernateCheckpoint Custom Resources                         │ │
│  │  - Koordiniert Checkpoint/Restore über Node Agents                      │ │
│  │  - Verwaltet Checkpoint Storage (S3, NFS, PVC)                          │ │
│  │  - Aktualisiert CR Status                                               │ │
│  │                                                                         │ │
│  │  Does NOT:                                                              │ │
│  │  - Direkten Zugriff auf containerd (läuft nicht auf jedem Node)        │ │
│  │  - CUDA Operationen (hat keine GPU)                                     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                    │                                         │
│                                    │ gRPC / HTTP                             │
│                                    ▼                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                    Kybernate Node Agent (DaemonSet)                     │ │
│  │                                                                         │ │
│  │  Läuft auf jedem Node mit:                                              │ │
│  │  - hostPID: true (Zugriff auf Host Prozesse)                            │ │
│  │  - Mounted: /run/containerd/containerd.sock                             │ │
│  │  - Mounted: /run/containerd/runc (Container State)                      │ │
│  │  - Mounted: /var/lib/containerd (Container Bundles)                     │ │
│  │  - NVIDIA GPU Zugriff (für CUDA API)                                    │ │
│  │                                                                         │ │
│  │  Responsibilities:                                                      │ │
│  │  - Führt CUDA Checkpoint/Restore aus                                    │ │
│  │  - Führt CRIU Checkpoint/Restore aus                                    │ │
│  │  - Speichert/Lädt Checkpoints                                           │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                    │                                         │
│                                    ▼                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                           containerd                                    │ │
│  │                                                                         │ │
│  │  ┌──────────────┐   ┌──────────────────┐   ┌──────────────────┐        │ │
│  │  │  runc-shim   │──►│ kybernate-runtime │──►│ nvidia-runtime   │        │ │
│  │  └──────────────┘   └──────────────────┘   └──────────────────┘        │ │
│  │         │                                            │                  │ │
│  │         │                                            ▼                  │ │
│  │         │                                   ┌──────────────────┐        │ │
│  │         │                                   │    Container     │        │ │
│  │         │                                   │   (GPU Prozess)  │        │ │
│  │         ▼                                   └──────────────────┘        │ │
│  │  ┌──────────────┐                                                       │ │
│  │  │ Task State   │  /run/containerd/runc/k8s.io/{container-id}/          │ │
│  │  │ - state.json │  - init_process_pid                                   │ │
│  │  │ - bundle ref │  - Bundle Path                                        │ │
│  │  └──────────────┘                                                       │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Custom Resource Definition

```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateCheckpoint
metadata:
  name: gpu-workload-checkpoint-001
  namespace: ml-training
spec:
  # Target Pod
  podName: pytorch-training-pod
  containerName: trainer  # Optional, default: erster Container
  
  # Action
  action: checkpoint  # oder: restore, delete
  
  # Checkpoint Storage
  storage:
    type: pvc  # oder: s3, nfs, hostPath
    pvcName: checkpoint-storage
    path: /checkpoints/pytorch-training-pod/20251204-193053
  
  # Restore Options (nur bei action: restore)
  restore:
    fromCheckpoint: gpu-workload-checkpoint-000  # Referenz auf vorherigen Checkpoint
    targetPodName: pytorch-training-pod-restored  # Optional: neuer Pod Name
    
  # Options
  options:
    leaveRunning: false  # Container nach Checkpoint stoppen?
    deleteAfterRestore: true  # Checkpoint nach Restore löschen?
    timeout: 300  # Sekunden

status:
  phase: Completed  # Pending, InProgress, Completed, Failed
  checkpointPath: /checkpoints/pytorch-training-pod/20251204-193053
  checkpointSize: 2.4Gi
  gpuMemoryCheckpointed: 2184Mi
  startTime: "2025-12-04T19:30:53Z"
  completionTime: "2025-12-04T19:31:15Z"
  message: "Checkpoint completed successfully"
  
  # Container Info (für Restore)
  containerInfo:
    originalContainerID: ab47f6411bad8d94c5e1941b6c21208bc5dd80f392af4ea72ebd6d6e7ebe8471
    originalPodUID: e811efe2-b4ad-4dfa-b7c9-2bb40c1a7462
    image: pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime
    runtimeClass: kybernate-gpu
```

---

## Checkpoint Flow (Detailliert)

### Schritt 1: User erstellt KybernateCheckpoint CR

```bash
kubectl apply -f - <<EOF
apiVersion: kybernate.io/v1alpha1
kind: KybernateCheckpoint
metadata:
  name: training-ckpt-001
  namespace: ml-training
spec:
  podName: pytorch-training
  action: checkpoint
  storage:
    type: pvc
    pvcName: checkpoint-pvc
EOF
```

### Schritt 2: Controller verarbeitet CR

```
Controller Watch Loop:
│
├─► Neues CR "training-ckpt-001" erkannt
│
├─► Validierung:
│   ├─ Pod existiert? ✓
│   ├─ Container läuft? ✓
│   ├─ RuntimeClass = kybernate-gpu? ✓
│   └─ Storage PVC verfügbar? ✓
│
├─► Node ermitteln:
│   └─ Pod läuft auf Node "gpu-worker-01"
│
├─► Status Update:
│   └─ status.phase = "InProgress"
│
└─► gRPC Call an Node Agent auf "gpu-worker-01":
    CheckpointRequest{
      Namespace: "ml-training",
      PodName: "pytorch-training",
      ContainerName: "trainer",
      StoragePath: "/mnt/checkpoint-pvc/training-ckpt-001",
    }
```

### Schritt 3: Node Agent führt Checkpoint aus

```
Node Agent auf gpu-worker-01:
│
├─► Container ID ermitteln:
│   └─ crictl ps → ab47f6411bad8d94...
│
├─► GPU Prozess finden:
│   ├─ State lesen: /run/containerd/runc/k8s.io/{id}/state.json
│   │   └─ init_process_pid: 633430
│   ├─ nvidia-smi --query-compute-apps=pid
│   │   └─ GPU PID: 633498 (Kind von 633430)
│   └─ GPU Memory: 2184 MiB
│
├─► STAGE 1: CUDA Checkpoint
│   ├─ cuCheckpointProcessLock(633498, timeout=60000ms)
│   │   └─ Blockiert alle CUDA API Calls im Prozess
│   ├─ cuCheckpointProcessCheckpoint(633498)
│   │   └─ VRAM (2184 MiB) → RAM Transfer
│   └─ GPU Memory jetzt: 0 MiB (VRAM frei!)
│
├─► STAGE 2: CRIU Checkpoint
│   ├─ runc checkpoint \
│   │     --root /run/containerd/runc/k8s.io \
│   │     --image-path /mnt/checkpoint-pvc/training-ckpt-001 \
│   │     ab47f6411bad8d94...
│   │
│   ├─ CRIU Dump:
│   │   ├─ Prozess-Speicher → pages-*.img (2.4 GB)
│   │   ├─ File Descriptors → fdinfo-*.img
│   │   ├─ Netzwerk State → netns-*.img
│   │   ├─ Cgroups → cgroup.img
│   │   └─ CUDA Plugin → cuda-checkpoint-*.img
│   │
│   └─ Container gestoppt (wenn leaveRunning=false)
│
├─► Metadata speichern:
│   └─ /mnt/checkpoint-pvc/training-ckpt-001/kybernate-metadata.json
│       {
│         "containerID": "ab47f6411bad8d94...",
│         "gpuPID": 633498,
│         "podSpec": { ... },  // Für Restore
│         "bundlePath": "/var/lib/containerd/.../bundle",
│         "timestamp": "2025-12-04T19:30:53Z"
│       }
│
└─► Response an Controller:
    CheckpointResponse{
      Success: true,
      CheckpointPath: "/mnt/checkpoint-pvc/training-ckpt-001",
      CheckpointSize: 2.4Gi,
      Duration: 22s,
    }
```

### Schritt 4: Controller aktualisiert Status

```yaml
status:
  phase: Completed
  checkpointPath: /mnt/checkpoint-pvc/training-ckpt-001
  checkpointSize: 2.4Gi
  completionTime: "2025-12-04T19:31:15Z"
```

---

## Restore Flow - Die drei Optionen

Das Restore ist der komplexe Teil. Es gibt drei mögliche Ansätze:

---

### Option A: containerd Task.Restore() API

#### Konzept

containerd hat eine eingebaute `Task` API mit Checkpoint/Restore Unterstützung. Die Idee ist, diese direkt zu nutzen.

#### Technischer Ablauf

```go
// 1. containerd Client erstellen
client, _ := containerd.New("/run/containerd/containerd.sock")
ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

// 2. Checkpoint Image laden
checkpoint, _ := client.GetImage(ctx, "checkpoint-image-ref")

// 3. Container erstellen (aus Original-Image)
container, _ := client.NewContainer(ctx, containerID,
    containerd.WithImage(originalImage),
    containerd.WithNewSnapshot(snapshotID, originalImage),
    containerd.WithNewSpec(oci.WithImageConfig(originalImage)),
)

// 4. Task mit Checkpoint starten
task, _ := container.NewTask(ctx, cio.NewCreator(cio.WithStdio),
    containerd.WithTaskCheckpoint(checkpoint),
)

// 5. Task starten (resumed from checkpoint)
task.Start(ctx)
```

#### Vorteile
- Native containerd Integration
- Saubere API
- Unterstützt von containerd Maintainern

#### Nachteile
- **Nicht über CRI exponiert!** Kubernetes/kubelet kann diese API nicht nutzen
- Erfordert direkten containerd Socket Zugriff
- Checkpoint muss als containerd Image vorliegen (nicht als Dateien)
- **Kubernetes weiß nichts vom wiederhergestellten Container** → Pod bleibt "Terminated"

#### Bewertung
⚠️ **Problematisch** - Funktioniert technisch, aber Kubernetes Integration fehlt.

---

### Option B: runc restore direkt

#### Konzept

`runc restore` kann einen Container direkt aus CRIU Checkpoint-Dateien wiederherstellen. Der Node Agent ruft runc direkt auf.

#### Technischer Ablauf

```bash
# 1. Original Bundle-Pfad ermitteln (aus Checkpoint Metadata)
BUNDLE_PATH="/var/lib/containerd/io.containerd.runtime.v2.task/k8s.io/${CONTAINER_ID}"

# 2. runc restore ausführen
runc restore \
  --root /run/containerd/runc/k8s.io \
  --bundle "$BUNDLE_PATH" \
  --image-path /mnt/checkpoint-pvc/training-ckpt-001 \
  --pid-file /tmp/restored.pid \
  ${NEW_CONTAINER_ID}
```

#### Das Bundle-Problem

runc restore benötigt:
1. **Bundle-Pfad** mit `config.json` (OCI Spec)
2. **rootfs** (Container Filesystem)

Diese existieren nur solange der Container/Pod existiert! Nach `kubectl delete pod`:
- Bundle wird gelöscht
- Snapshot wird gelöscht
- rootfs ist weg

#### Lösungsansatz: Bundle rekonstruieren

```
Restore-Prozess:
│
├─► 1. Container Image pullen (aus Checkpoint Metadata)
│   └─ docker.io/pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime
│
├─► 2. Neuen Snapshot erstellen
│   └─ containerd snapshots prepare --target /tmp/restore-rootfs
│
├─► 3. config.json rekonstruieren
│   └─ Aus gespeicherter Pod Spec + Container Config
│
├─► 4. Bundle-Verzeichnis erstellen
│   /tmp/restore-bundle/
│   ├── config.json  (rekonstruiert)
│   └── rootfs/      (vom Snapshot)
│
├─► 5. runc restore ausführen
│   └─ runc restore --bundle /tmp/restore-bundle ...
│
└─► 6. Prozess läuft wieder!
    └─ Aber: containerd/Kubernetes wissen nichts davon
```

#### Vorteile
- Direkter CRIU Restore
- Volle Kontrolle über den Prozess
- Funktioniert unabhängig von höheren APIs

#### Nachteile
- **Bundle-Rekonstruktion komplex** - config.json hat viele Felder
- **Container nicht in containerd registriert** - "Geister-Container"
- **Pod bleibt in Kubernetes als "Terminated"**
- Netzwerk, Volumes, etc. müssen manuell gehandhabt werden

#### Bewertung
⚠️ **Komplex** - Machbar, aber viel manuelle Arbeit und keine K8s Integration.

---

### Option C: Neuen Pod erstellen + Process State injizieren

#### Konzept

Statt den Container direkt zu restoren, erstellen wir einen **neuen Pod** über die Kubernetes API und injizieren dann den Checkpoint-State in den laufenden Prozess.

#### Technischer Ablauf

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           RESTORE FLOW - OPTION C                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Phase 1: Pod Creation (Kubernetes-native)                                   │
│  ──────────────────────────────────────────                                  │
│                                                                              │
│  1. Controller liest Original Pod Spec aus Checkpoint Metadata               │
│     └─ /mnt/checkpoint-pvc/training-ckpt-001/kybernate-metadata.json        │
│                                                                              │
│  2. Controller erstellt neuen Pod mit modifizierter Spec:                    │
│     apiVersion: v1                                                           │
│     kind: Pod                                                                │
│     metadata:                                                                │
│       name: pytorch-training-restored                                        │
│       annotations:                                                           │
│         kybernate.io/restore-from: training-ckpt-001                        │
│         kybernate.io/restore-phase: pending                                  │
│     spec:                                                                    │
│       runtimeClassName: kybernate-gpu  # WICHTIG!                           │
│       initContainers:                                                        │
│       - name: restore-preparer                                               │
│         image: kybernate/restore-agent:v1                                    │
│         command: ["prepare-restore"]                                         │
│         volumeMounts:                                                        │
│         - name: checkpoint                                                   │
│           mountPath: /checkpoint                                             │
│       containers:                                                            │
│       - name: trainer                                                        │
│         image: pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime                │
│         # MODIFIED: Start paused, waiting for restore                       │
│         command: ["/kybernate/restore-wrapper"]                             │
│         args: ["--wait-for-restore", "--original-cmd", "python train.py"]  │
│                                                                              │
│  3. Kubernetes scheduled Pod auf GPU Node                                    │
│     └─ Container startet, wartet auf Restore-Signal                         │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Phase 2: Process State Injection                                            │
│  ─────────────────────────────────                                           │
│                                                                              │
│  4. Node Agent erkennt neuen Pod mit restore-Annotation                      │
│                                                                              │
│  5. Node Agent führt CRIU "restore-in-place" aus:                            │
│                                                                              │
│     Option C.1: CRIU criu-ns restore (Namespace restore)                     │
│     ─────────────────────────────────────────────────────                    │
│     # Restore innerhalb des existierenden Containers                        │
│     criu restore \                                                           │
│       --images-dir /checkpoint \                                             │
│       --pidns \                                                              │
│       --root /proc/${CONTAINER_PID}/root                                    │
│                                                                              │
│     Option C.2: Process Memory Replacement                                   │
│     ─────────────────────────────────────────────────────                    │
│     # Speicher des laufenden Prozesses mit Checkpoint überschreiben         │
│     1. Attach an Container-Prozess (ptrace)                                  │
│     2. Speicherregionen mappen aus pages-*.img                              │
│     3. Register wiederherstellen aus core-*.img                             │
│     4. File Descriptors wiederherstellen                                    │
│     5. Detach und Continue                                                   │
│                                                                              │
│     Option C.3: execve() mit Checkpoint                                      │
│     ─────────────────────────────────────────────────────                    │
│     # Container-Entrypoint ruft speziellen Restore-Loader                   │
│     /kybernate/restore-loader --checkpoint /checkpoint/                     │
│     → Loader lädt pages-*.img in eigenen Speicher                           │
│     → Springt zu wiederhergestelltem Instruction Pointer                    │
│                                                                              │
│  6. Nach Memory Restore: CUDA Restore                                        │
│     └─ cuCheckpointProcessRestore(newPID)                                   │
│     └─ cuCheckpointProcessUnlock(newPID)                                    │
│                                                                              │
│  7. Prozess läuft weiter wo er aufgehört hat!                               │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Endergebnis:                                                                │
│  - Pod "pytorch-training-restored" läuft in Kubernetes ✓                    │
│  - Alle K8s Features funktionieren (Logs, Exec, Port-Forward) ✓            │
│  - GPU Memory wiederhergestellt ✓                                           │
│  - Application State wiederhergestellt ✓                                    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Die drei Sub-Optionen für Process Injection

##### Option C.1: CRIU criu-ns restore

```bash
# CRIU kann in existierende Namespaces restoren
criu restore \
  --images-dir /checkpoint \
  --inherit-fd fd[0]:/dev/stdin \
  --inherit-fd fd[1]:/dev/stdout \
  --inherit-fd fd[2]:/dev/stderr \
  --pidns \          # In existierenden PID Namespace
  --mntns \          # In existierenden Mount Namespace  
  --netns \          # In existierenden Network Namespace
  --exec-cmd \       # Ersetze aktuellen Prozess
  -- /original/entrypoint
```

**Problem**: CRIU erwartet, dass es den Prozess-Baum selbst erstellt. "Restore into existing process" ist nicht das Standardverhalten.

##### Option C.2: Process Memory Replacement (ptrace)

```c
// Konzept: Speicher eines laufenden Prozesses ersetzen

// 1. An Prozess attachen
ptrace(PTRACE_ATTACH, target_pid, NULL, NULL);

// 2. Alle Memory Mappings lesen
// /proc/{pid}/maps

// 3. Speicher aus Checkpoint laden
void* checkpoint_pages = mmap_checkpoint_file("pages-1.img");

// 4. Speicher in Zielprozess schreiben
process_vm_writev(target_pid, local_iov, remote_iov, ...);

// 5. Register wiederherstellen
struct user_regs_struct regs = load_from_checkpoint("core-1.img");
ptrace(PTRACE_SETREGS, target_pid, NULL, &regs);

// 6. Prozess fortsetzen
ptrace(PTRACE_DETACH, target_pid, NULL, NULL);
```

**Problem**: Sehr komplex, fehleranfällig, File Descriptors sind schwierig.

##### Option C.3: Restore Loader (Empfohlen)

```
Container startet mit speziellem Entrypoint:

┌─────────────────────────────────────────────────────────────────┐
│  /kybernate/restore-loader                                      │
│                                                                  │
│  1. Prüft ob Checkpoint vorhanden                               │
│     └─ /checkpoint/kybernate-metadata.json exists?              │
│                                                                  │
│  2. Wenn ja: Restore-Modus                                      │
│     ├─ Öffnet pages-*.img Files                                 │
│     ├─ Mappt Speicher an Original-Adressen (mmap mit MAP_FIXED) │
│     ├─ Lädt Register-State aus core-*.img                       │
│     ├─ Rekonstruiert File Descriptors                           │
│     └─ longjmp() zum gespeicherten Instruction Pointer          │
│                                                                  │
│  3. Wenn nein: Normal-Modus                                     │
│     └─ execve(original_entrypoint, original_args, env)          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Vorteil**: Der Prozess "restored sich selbst" ohne externe Eingriffe.

**Problem**: CRIU page format ist komplex, viele Edge Cases.

#### Vorteile von Option C
- **Kubernetes-native** - Pod ist regulärer K8s Pod
- **Alle K8s Features** - Logs, Exec, Port-Forward, etc.
- **Networking automatisch** - K8s CNI handled Netzwerk
- **Volumes automatisch** - K8s mounted PVCs

#### Nachteile von Option C
- **Komplexer Restore-Loader** benötigt
- **Memory Layout muss exakt übereinstimmen**
- **ASLR kann problematisch sein** (Address Space Layout Randomization)

#### Bewertung
✅ **Empfohlen** - Beste Kubernetes Integration, aber Restore-Loader ist komplex.

---

## Empfohlene Implementierungsstrategie

### Hybrid-Ansatz: Option B + C

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    HYBRID RESTORE STRATEGY                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Schritt 1: Pod-Hülle über Kubernetes erstellen                             │
│  ─────────────────────────────────────────────────                          │
│  - Neuer Pod mit gleicher Spec wie Original                                 │
│  - Spezielle Annotation: kybernate.io/restore-from                          │
│  - Container startet PAUSED (via restore-wrapper)                           │
│  → Kubernetes handled: Scheduling, Networking, Volumes                      │
│                                                                              │
│  Schritt 2: runc restore in den Pod-Container                               │
│  ─────────────────────────────────────────────────                          │
│  - Node Agent wartet bis Pod "Running" ist                                  │
│  - Stoppt den Pause-Prozess im Container                                    │
│  - Führt runc restore mit Bundle des neuen Containers aus                   │
│  - CRIU restored den Prozess IN den existierenden Container                 │
│                                                                              │
│  Schritt 3: CUDA Restore                                                    │
│  ─────────────────────────────────────────────────                          │
│  - cuCheckpointProcessRestore(newPID)                                       │
│  - cuCheckpointProcessUnlock(newPID)                                        │
│  → GPU Memory wiederhergestellt                                             │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Detaillierter Ablauf

```
1. Controller erstellt Pod:
   ┌─────────────────────────────────────────────────────────────────────────┐
   │ apiVersion: v1                                                           │
   │ kind: Pod                                                                │
   │ metadata:                                                                │
   │   name: training-restored                                                │
   │   annotations:                                                           │
   │     kybernate.io/restore-from: training-ckpt-001                        │
   │ spec:                                                                    │
   │   runtimeClassName: kybernate-gpu                                        │
   │   containers:                                                            │
   │   - name: trainer                                                        │
   │     image: pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime                │
   │     command: ["/bin/sh", "-c", "sleep infinity"]  # Pause!              │
   │     volumeMounts:                                                        │
   │     - name: checkpoint                                                   │
   │       mountPath: /checkpoint                                             │
   │       readOnly: true                                                     │
   │   volumes:                                                               │
   │   - name: checkpoint                                                     │
   │     persistentVolumeClaim:                                               │
   │       claimName: checkpoint-pvc                                          │
   └─────────────────────────────────────────────────────────────────────────┘

2. Kubernetes scheduled Pod → Container startet mit "sleep infinity"

3. Node Agent erkennt Pod mit restore-Annotation:
   - Wartet bis status.phase == Running
   - Ermittelt Container ID: xyz789...
   - Ermittelt Bundle Path: /var/lib/containerd/.../xyz789.../

4. Node Agent stoppt sleep-Prozess und führt Restore aus:
   ┌─────────────────────────────────────────────────────────────────────────┐
   │ # PID des sleep-Prozesses                                               │
   │ SLEEP_PID=$(cat /run/containerd/runc/k8s.io/$CID/state.json |          │
   │             jq .init_process_pid)                                       │
   │                                                                          │
   │ # Prozess stoppen (SIGSTOP)                                             │
   │ kill -STOP $SLEEP_PID                                                   │
   │                                                                          │
   │ # CRIU Restore im Container-Kontext                                     │
   │ # Option: nsenter + criu restore                                        │
   │ nsenter -t $SLEEP_PID -p -m -n \                                        │
   │   criu restore \                                                         │
   │     --images-dir /checkpoint \                                          │
   │     --shell-job \                                                        │
   │     --restore-detached                                                   │
   │                                                                          │
   │ # Oder: runc restore mit existierendem Bundle                           │
   │ runc restore \                                                           │
   │   --root /run/containerd/runc/k8s.io \                                  │
   │   --bundle $BUNDLE_PATH \                                                │
   │   --image-path /checkpoint \                                             │
   │   --detach \                                                             │
   │   $NEW_CONTAINER_ID                                                      │
   └─────────────────────────────────────────────────────────────────────────┘

5. CUDA Restore:
   ┌─────────────────────────────────────────────────────────────────────────┐
   │ // Neuer GPU-Prozess hat State "checkpointed" geerbt                    │
   │ ckpt := cuda.NewCheckpointer()                                          │
   │ ckpt.Restore(newGPUPid)  // RAM → VRAM                                  │
   │ ckpt.Unlock(newGPUPid)   // Resume CUDA calls                           │
   └─────────────────────────────────────────────────────────────────────────┘

6. Prozess läuft weiter!
   - Loop counter: 10, 11, 12, ... (fortgesetzt von Checkpoint)
   - GPU Tensor: Werte wiederhergestellt
   - Pod in Kubernetes: "Running", alle Features funktionieren
```

---

## Implementierungs-Reihenfolge

### Phase 1: CRD und Controller Grundgerüst
1. KybernateCheckpoint CRD definieren
2. Controller Deployment mit client-go
3. Watches auf KybernateCheckpoint CRs
4. Basis Status Updates

### Phase 2: Node Agent
1. DaemonSet mit containerd Socket Mount
2. gRPC Server für Controller-Kommunikation
3. Checkpoint-Logik (existiert bereits in kybernate-ctl)
4. Storage Upload/Download

### Phase 3: Restore Implementation
1. Pod-Hülle erstellen (Controller)
2. Restore-Detection im Node Agent
3. nsenter + CRIU restore
4. CUDA Restore nach CRIU

### Phase 4: Testing und Hardening
1. E2E Tests mit GPU Workloads
2. Failure Handling
3. Timeout Management
4. Cleanup von alten Checkpoints

---

## Offene Fragen

1. **Wie handled man PIDs?**
   - Der wiederhergestellte Prozess hat eine neue PID
   - Wie registriert man das bei containerd?

2. **File Descriptor Handling**
   - Checkpoint hat FDs vom Original-Container
   - Neuer Container hat andere FDs
   - Wie mapped man diese?

3. **Netzwerk-Verbindungen**
   - TCP Connections können nicht restored werden
   - Applikation muss damit umgehen können

4. **Shared Memory / IPC**
   - Wie restored man System V IPC?
   - CUDA IPC Handles?

5. **Multi-Container Pods**
   - Müssen alle Container gleichzeitig checkpointed werden?
   - Oder nur der GPU-Container?

---

## Fazit

Der empfohlene Ansatz ist **Option C (Hybrid)**: Kubernetes erstellt die Pod-Infrastruktur, und CRIU/runc restored den Prozess-State in den laufenden Container. Dies kombiniert:

- ✅ Kubernetes-native Pod-Verwaltung
- ✅ Voller Zugriff auf K8s Features (Logs, Exec, Networking)
- ✅ CRIU Restore für Process State
- ✅ CUDA Restore für GPU Memory

Die größte Herausforderung ist das korrekte Ersetzen des "sleep infinity" Prozesses durch den CRIU-restored Prozess im gleichen Container-Kontext.
