# Restore-Variante B: Neuer Pod + Restore-Annotation (Option C)

## Überblick

Diese Variante entspricht der im Haupt-Design als **Option C** beschriebenen Strategie: Anstatt den ursprünglichen Pod/Container direkt auf Low-Level-Ebene zu restoren (containerd/runc), wird ein **neuer Pod** erstellt, der über Annotationen/Env-Variablen anzeigt, dass sein Prozesszustand aus einem zuvor erstellten Checkpoint geladen werden soll.

Damit bleibt Kubernetes im gewohnten Modell („Pods werden erstellt und laufen“), während die eigentliche Restore-Logik in der Runtime (`kybernate-runtime`) und im CUDA-Layer gekapselt ist.

## Architekturkomponenten

- **Kybernate Controller (Deployment)**
  - Beobachtet `KybernateCheckpoint`-Custom Resources.
  - Orchestriert Checkpoint- und Restore-Vorgänge.

- **Kybernate Node-Komponenten**
  - `kybernate-runtime` (als `RuntimeClass` in Pods verwendet):
    - Interceptiert containerd/runc-Calls beim Container-Start.
    - Erkennt, ob ein Restore gewünscht ist (Annotation/Env).
    - Ruft CRIU- und CUDA-Restore auf.
  - CUDA-Bindings (`shim/pkg/cuda/checkpoint.go`):
    - Bieten die GPU-Checkpoint/Restore-Funktionalität.

- **Checkpoint-Speicher**
  - PVC, NFS, S3 o.ä., über den der Controller und die Nodes auf Checkpoint-Verzeichnisse zugreifen können.

## Datenfluss im Überblick

1. **Checkpoint-Phase** (bereits implementiert)
   - `KybernateCheckpoint` mit `spec.action: checkpoint` erstellt.
   - Node-seitiger Controller/Runtime führt Two-Stage-Checkpoint durch:
     1. CUDA-Checkpoint (VRAM → RAM).
     2. CRIU-Checkpoint (RAM → Disk) via Kubernetes Checkpoint API / runc.
   - Ergebnis: Checkpoint-Verzeichnis inkl. Metadaten (z.B. `kybernate-metadata.json`).

2. **Restore-Phase** (diese Variante)
   - `KybernateCheckpoint` mit `spec.action: restore` und Verweis auf existierenden Checkpoint.
   - Controller erstellt neuen Pod mit Restore-Annotation/Env.
   - `kybernate-runtime` führt CRIU-Restore + CUDA-Restore aus.

## Detaillierter Restore-Flow

### 1. `KybernateCheckpoint`-CR für Restore

Beispiel:

```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateCheckpoint
metadata:
  name: gpu-workload-checkpoint-restore-001
  namespace: ml-training
spec:
  action: restore
  podName: pytorch-training-pod
  containerName: trainer
  storage:
    type: pvc
    pvcName: checkpoint-storage
    path: /checkpoints/pytorch-training-pod/20251204-193053
  restore:
    fromCheckpoint: gpu-workload-checkpoint-000
    targetPodName: pytorch-training-pod-restored
  options:
    deleteAfterRestore: false
```

### 2. Controller-Logik (Control Plane)

1. **CR einlesen und validieren**
   - Existiert das referenzierte Checkpoint-Objekt?
   - Ist `spec.action == restore`?
   - Existiert das angegebene Storage-Pfad/Verzeichnis?

2. **Checkpoint-Metadaten lesen**
   - Datei z.B. `kybernate-metadata.json` im Checkpoint-Pfad:

```json
{
  "containerID": "ab47f6411bad8d94...",
  "gpuPID": 633498,
  "podSpec": { "...": "..." },
  "bundlePath": "/var/lib/containerd/.../bundle",
  "timestamp": "2025-12-04T19:30:53Z"
}
```

   - Wichtig sind insbesondere:
     - Ursprüngliches Container-Image.
     - Ressourcen (CPU, Memory, GPU requests/limits).
     - Volumes/VolumeMounts.
     - Environment-Variablen und Kommandozeilenargumente.

3. **Neuen Pod-Spec erzeugen**

   - Basis: `podSpec` aus den Metadaten oder direkt aus dem ursprünglichen Pod (über Kubernetes-API).
   - Änderungen:
     - Neuer `metadata.name` (z.B. `spec.restore.targetPodName`).
     - Annotation auf dem Zielcontainer (oder Pod):

```yaml
metadata:
  annotations:
    kybernate.io/restore-from: /checkpoints/pytorch-training-pod/20251204-193053
```

     - Alternativ/ergänzend: Env-Var im Container:

```yaml
spec:
  containers:
    - name: trainer
      env:
        - name: RESTORE_FROM
          value: /checkpoints/pytorch-training-pod/20251204-193053
```

     - `runtimeClassName: kybernate-gpu` sicherstellen.

4. **Pod erstellen**
   - Über Kubernetes-API (`client-go`) oder `kubectl apply`:
     - Pod-Objekt in `Running` bringen.

5. **Pod-Status überwachen**
   - Auf Phasen-Übergänge des neuen Pods warten (`Pending` → `Running` oder Fehlerzustand).
   - Bei Fehlern Events/Logs anhängen und `KybernateCheckpoint.status` entsprechend setzen.

6. **Status-Update des `KybernateCheckpoint`**

```yaml
status:
  phase: Completed
  message: "Restore pod pytorch-training-pod-restored running"
  restoreInfo:
    restoredPodName: pytorch-training-pod-restored
    checkpointPath: /checkpoints/pytorch-training-pod/20251204-193053
```

### 3. Runtime-Verhalten (`kybernate-runtime` + Shim)

Die Datei `shim/pkg/service/service.go` enthält die entscheidende Logik im `Create`-Hook:

- Beim Container-Start wird die OCI-Spec (`config.json`) geladen.
- Es werden zwei Mechanismen verwendet, um einen Restore zu erkennen:
  - Annotation: `spec.Annotations["kybernate.io/restore-from"]`.
  - Env: `RESTORE_FROM=<checkpointPath>`.
- Falls eine dieser Quellen gesetzt ist:
  - `req.Checkpoint = <checkpointPath>` wird konfiguriert.
  - Der unterliegende runc-Shim führt einen **CRIU-Restore** aus den Dateien in `<checkpointPath>` durch.
- Nach erfolgreichem `Create`:
  - Wird kurz gewartet, bis der Task-Prozess läuft.
  - Über `cuda.FindAnyGPUProcessForTask` wird der GPU-Prozess gesucht.
  - Dessen CUDA-Zustand wird abgefragt (`GetState`).
  - Bei `StateCheckpointed` wird `RestoreFull` ausgeführt (RAM → VRAM), sodass GPU-Speicher wiederhergestellt wird.

Damit ist die Restore-Kette für GPU-Workloads vollständig:

1. Neuer Pod mit Restore-Annotation/Env.
2. CRIU-Restore durch runc/kybernate-runtime.
3. CUDA-Restore durch den CUDA-Checkpointer.

## Vor- und Nachteile der Variante B (Option C)

### Vorteile

- **Kubernetes-konform:**
  - Der Restore erfolgt durch Erstellen eines neuen Pods, den Kubernetes vollständig kennt und verwaltet.
  - Standards wie `kubectl get pods`, Events, Logs etc. funktionieren wie gewohnt.
- **Klare Verantwortlichkeit:**
  - Control Plane: Controller orchestriert nur CRs und Pods.
  - Node-Ebene: Runtime/Shim und CUDA-Bindings kümmern sich um Low-Level-Details.
- **Gute Testbarkeit:**
  - Man kann Restore isoliert testen, indem man Pods manuell mit der Restore-Annotation/Env startet.

### Nachteile / Herausforderungen

- **Metadaten-Komplexität:**
  - Der Controller muss ausreichend Informationen über den ursprünglichen Pod speichern, um einen sinnvollen Restore-Pod spezifizieren zu können (Volumes, SecurityContext, Ressourcen, etc.).
- **Latenz:**
  - Ein kompletter Pod-Neustart ist langsamer als ein reiner Prozess-Restore über containerd.
- **Lebenszyklus-Management:**
  - Checkpoints (besonders große GPU-Checkpoints) müssen sauber verwaltet und ggf. nach erfolgreichem Restore gelöscht werden (`deleteAfterRestore`).

## Typische Test-Strategie für Variante B

1. **Manueller Test ohne Controller:**
   - Einen existierenden GPU-Pod mit kybernate-gpu-runtime checkpointen (z.B. über `kybernate-ctl` oder Testscripte).
   - Ein Restore-Pod-Manifest mit Annotation `kybernate.io/restore-from: <checkpointPath>` schreiben.
   - Pod deployen und prüfen, ob die Anwendung ihren Zustand korrekt fortsetzt (z.B. Trainingstep, Inference-Resultate, GPU-Utilization).

2. **Integrationstest mit Controller:**
   - `KybernateCheckpoint`-CRs für `checkpoint` und `restore` erstellen.
   - Controller ausrollen und beobachten, ob Restore-Pods wie erwartet erzeugt werden.
   - Statusfelder (`status.phase`, `status.message`, `status.restoreInfo`) evaluieren.

## Abgrenzung zu Variante A

- **Ansatz:**
  - Variante A arbeitet direkt mit containerd-Tasks und ist aus K8s-Sicht unsichtbar.
  - Variante B nutzt reguläre Pods und `RuntimeClass` und fügt Restore-Informationen nur über Annotation/Env hinzu.
- **Einsatzgebiet:**
  - Variante A eignet sich für Low-Level-Experimente und Vergleiche.
  - Variante B ist die bevorzugte Basis für eine produktive Kubernetes-Integration.
