# Restore-Variante A: containerd Task.Restore()

## Überblick

Diese Variante nutzt die native Task-API von containerd, um einen Container direkt aus einem Checkpoint wiederherzustellen. Die CRIU/Checkpoint-Daten müssen dabei als containerd-Checkpoint-Image vorliegen, das über die containerd-Client-API geladen wird. Kubernetes selbst kennt diese Operation nicht; der Restore passiert **unterhalb** des CRI.

Ziel dieser Datei ist es, die Variante A so detailliert zu beschreiben, dass sie isoliert implementiert und getestet werden kann, ohne mit Variante B/C zu kollidieren.

## Architektur und Rahmenbedingungen

- **Ort der Implementierung:**
  - Läuft typischerweise in einem Node-nahen Agent (DaemonSet) mit Zugriff auf `/run/containerd/containerd.sock`.
- **Schnittstellen:**
  - Verwendet die Go-API von containerd (`github.com/containerd/containerd`).
  - Kein direkter CRI-Zugriff, keine kubelet-Erweiterung.
- **Sichtbarkeit in Kubernetes:**
  - Kubernetes „weiß“ nichts vom wiederhergestellten Task.
  - Der ursprüngliche Pod/Container-Status ändert sich nicht; aus Sicht von K8s bleibt der Pod z.B. `Terminated`.

## Technischer Ablauf

### 1. Voraussetzungen

- Der ursprüngliche Checkpoint liegt als containerd-Checkpoint-Image vor, z.B. unter einem Image-Ref wie `k8s.io/pytorch-training-checkpoint:001`.
- Metadaten über das ursprüngliche Image und die OCI-Spec sind vorhanden, z.B. aus einer Metadatendatei:

```json
{
  "originalImage": "docker.io/pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime",
  "snapshotter": "overlayfs",
  "snapshotID": "pytorch-training-rootfs",
  "ociSpec": { "...": "..." }
}
```

### 2. Restore-Flow im Node-Agent

1. **containerd-Client erstellen**
   - Verbindung zu `/run/containerd/containerd.sock`.
   - Nutzung des `k8s.io`-Namespaces.
2. **Checkpoint-Image auflösen**
   - `client.GetImage(ctx, checkpointRef)`
3. **Neuen Container erzeugen**
   - `client.NewContainer(...)` mit:
     - `WithImage(originalImage)`
     - `WithNewSnapshot(snapshotID, originalImage)`
     - `WithNewSpec(...)` (basierend auf ursprünglicher OCI-Spec).
4. **Task aus Checkpoint starten**
   - `container.NewTask(ctx, ...)` mit Option `WithTaskCheckpoint(checkpoint)`.
5. **Task starten**
   - `task.Start(ctx)` – der Prozess läuft nun mit dem aus dem Checkpoint rekonstruierten Zustand.

### 3. Beispielcode (vereinfachter Ausschnitt)

```go
client, err := containerd.New("/run/containerd/containerd.sock")
if err != nil { /* handle */ }

defer client.Close()

ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

// 1. Checkpoint-Image laden
checkpoint, err := client.GetImage(ctx, checkpointRef)
if err != nil { /* handle */ }

// 2. Original-Image auflösen
image, err := client.GetImage(ctx, originalImageRef)
if err != nil { /* handle */ }

// 3. Container erzeugen
container, err := client.NewContainer(
  ctx,
  newContainerID,
  containerd.WithImage(image),
  containerd.WithNewSnapshot(snapshotID, image),
  containerd.WithNewSpec(oci.WithImageConfig(image)),
)
if err != nil { /* handle */ }

// 4. Task aus Checkpoint erzeugen
task, err := container.NewTask(
  ctx,
  cio.NewCreator(cio.WithStdio),
  containerd.WithTaskCheckpoint(checkpoint),
)
if err != nil { /* handle */ }

// 5. Start















































  - Eignet sich eher für Low-Level-Tests oder Spezialfälle als für eine produktive K8s-Integration.  - Läuft vollständig „unterhalb“ von Kubernetes; es wird kein neuer Pod erstellt.- **Gegenüber Option C (neuer Pod + Annotation)**:  - Nutzt höhere Abstraktion (containerd-API) statt direkt runc aufzurufen und Bundles manuell zu rekonstruieren.- **Gegenüber Option B (runc restore)**:## Abgrenzung zu den anderen Varianten  - Variante A ausschließlich zum Vergleich einsetzen, ohne den Pod-Status zu benutzen – z.B. um zu zeigen, dass ein Prozess-Zustand technisch wiederhergestellt werden kann, auch wenn K8s das nicht sieht.- In Kombination mit Kubernetes:  3. Prüfen, ob der Prozess korrekt resumed (z.B. durch Applikations-Logging oder CPU/GPU-Last).  2. Node-Agent/Tool mit o.g. Flow aufrufen.  1. Manuell ein containerd-Checkpoint-Image erstellen.- Isolierte Tests außerhalb von Kubernetes (oder auf einem Dev-Node):## Typische Test-Strategie für Variante A  - Der Node-Agent muss selbst aufräumen (Tasks stoppen, Container löschen, Snapshots entfernen).- **Lebenszyklus-Management:**  - Checkpoints müssen als containerd-Images vorliegen, nicht nur als rohe CRIU-Dateien.- **Checkpoint-Format:**  - Monitoring, Log-Aggregation und kubectl-Befehle bleiben auf dem alten Pod stehen.  - Der wiederhergestellte Task taucht nicht in Pod/Container-Status von Kubernetes auf.- **Keine Kubernetes-Sichtbarkeit:**### Nachteile- **Feinsteuerung auf Node-Ebene:** Node-Agent kann sehr präzise steuern, welcher Container/Task wie restored wird.- **Kein Eingriff in kubelet/CRI nötig:** Der Restore findet komplett auf containerd-Ebene statt.- **Saubere containerd-Integration:** Nutzt offiziell unterstützte APIs von containerd.### Vorteile## Vor- und Nachteile der Variante ADies entspricht funktional der zweiten Stage (CUDA-Restore) aus Option C, ist aber hier losgelöst von kubelet/CRI.3. Die CUDA-Restore-API (`cuCheckpointProcessRestore`) aufrufen, um VRAM aus dem Host-RAM wiederherzustellen.2. Mittels `nvidia-smi` oder bestehender Helper-Funktionen einen GPU-Prozess innerhalb des Cgroups-Kontexts finden.1. Den Hauptprozess-PID abfragen (`task.Pid()`).In dieser Variante ist GPU-Restore optional und nicht zwingend an Kubernetes gekoppelt. Ein Node-Agent könnte nach `task.Start`:## CUDA/GPU-Integration (optional)```if err := task.Start(ctx); err != nil { /* handle */ }