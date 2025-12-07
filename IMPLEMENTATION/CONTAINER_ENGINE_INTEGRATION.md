# Container Engine Integration Strategy

Dieses Dokument klärt die Interaktion mit der Container Runtime (Containerd) und den Low-Level Tools (runc, CRIU) für den CPU/Memory-Teil des Checkpoints.

## Entscheidung: Containerd API vs. Runc vs. CRIU

### 1. CPU/Memory Checkpoint: Nutzung der Containerd API
Für den Checkpoint des Container-Prozesses (CPU, RAM, File Descriptors) soll **ausschließlich die Containerd Go API** verwendet werden.

*   **Status:** Containerd v1.7+ (in MicroK8s enthalten) unterstützt `task.Checkpoint()` vollständig.
*   **Warum?**
    *   `containerd` verwaltet den State der Container. Ein direkter Aufruf von `runc` oder `criu` am `containerd` vorbei führt zu Inkonsistenzen ("Split Brain").
    *   Die `containerd` API abstrahiert die Komplexität von Namespaces und Cgroups.

*   **Implementierung (Node Agent):**
    *   Import: `github.com/containerd/containerd`
    *   Verbindung: `/run/containerd/containerd.sock`
    *   Call: `task.Checkpoint(ctx, options...)`

### 2. CPU/Memory Restore: Shim-Level Integration (runc/libcontainer)
Obwohl Containerd eine Restore-API (`NewTask(..., WithCheckpoint)`) besitzt, kann diese nicht direkt von Kubernetes (CRI) angesprochen werden.

*   **Strategie:** "New Pod" Pattern.
*   **Implementierung (Containerd Shim):**
    *   Der Shim fängt den `Create`-Call von Containerd ab.
    *   Er erkennt die Restore-Annotation (`kybernate.io/restore-from`).
    *   Anstatt einen neuen Container zu starten, führt er einen **Restore** durch.
    *   **Technik:** Hier ist der direkte Aufruf von `runc restore` (oder Nutzung von `libcontainer` in Go) innerhalb des Shims notwendig, da wir den Standard-Lifecycle von Containerd an dieser Stelle modifizieren.

### 3. GPU Checkpoint: Direkte CUDA Driver API
Wie in `components/node-agent.md` definiert, erfolgt der GPU-Teil **nicht** über `containerd` (da dieses keine GPU-States kennt), sondern **direkt über die NVIDIA Driver API** im Node Agent, *bevor* der Containerd-Checkpoint getriggert wird.

### 3. Rolle von `crictl`
`crictl` ist ein CLI-Tool für das Debugging von CRI-kompatiblen Runtimes.
*   **Verwendung:** Nur für manuelles Debugging und Verifikation durch Entwickler.
*   **Keine Verwendung im Code:** Der Node Agent oder Operator soll `crictl` **nicht** aufrufen (kein Shell-Out).

### 4. Rolle der Kubernetes Checkpoint API (Kubelet)
Kubernetes bietet seit v1.25 eine Beta-API für Checkpointing (`/checkpoint` endpoint am Kubelet).
*   **Bewertung:** Diese API ist nützlich, erlaubt aber aktuell keine feingranulare Koordination mit dem GPU-Locking (Pre-Checkpoint Hooks sind limitiert).
*   **Strategie:** Kybernate nutzt primär den direkten Weg über den Node Agent und die Containerd API, um die strikte Reihenfolge (CUDA Lock -> VRAM Dump -> CPU Checkpoint) zu garantieren.

## Zusammenfassung des Flows

1.  **Operator** sendet `CheckpointRequest` an **Node Agent**.
2.  **Node Agent** (via CGO):
    *   Ruft `cuCheckpointSave` auf (VRAM -> RAM).
3.  **Node Agent** (via Containerd API):
    *   Ruft `containerd.LoadContainer(id)`
    *   Ruft `task.Checkpoint()` (erzeugt CPU/Memory Images via runc/criu).
4.  **Node Agent**:
    *   Sammelt GPU-Dumps und CRIU-Images.
    *   Übergibt alles an **Sidecar** zum Upload.
