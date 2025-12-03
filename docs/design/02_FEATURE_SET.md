# Kybernate: Erweitertes Feature Set & Vision

Basierend auf der Analyse der Anforderungen und Möglichkeiten, die sich durch GPU-Checkpointing (CRIU + CUDA-Checkpoint) ergeben, definieren wir hier das vollständige Feature-Set für die "Kybernate"-Plattform.

Das Kernparadigma verschiebt sich von **"GPU als statische Ressource"** hin zu **"VRAM als Cache für aktive Workloads"**.

## 1. Core Features: Workload Lifecycle Management

Diese Features betreffen den direkten Umgang mit einzelnen Containern/Pods.

### 1.1. Instant Pause & Resume (Suspend)
*   **Funktion**: Ein laufender Prozess (Training, Inferenz, Quantisierung) wird "eingefroren".
*   **Vorteil**: Der VRAM wird zu 100% freigegeben. Die GPU-Compute-Unit steht sofort anderen Prozessen zur Verfügung.
*   **Status**: Der Prozess existiert nicht mehr auf der GPU, sein Zustand liegt im System-RAM (oder auf Disk).

### 1.2. Hierarchical Storage Tiering (HSM für GPU States)
Workloads können in verschiedenen "Tiefen" geparkt werden, abhängig von der benötigten Wiederherstellungszeit:
*   **Tier 0 (Active)**: Läuft im VRAM.
*   **Tier 1 (Hot Standby)**: Checkpoint liegt im **System-RAM (tmpfs)**.
    *   *Restore-Zeit*: < 1-2 Sekunden (limitiert durch PCIe Bandbreite).
    *   *Use-Case*: Scale-to-Zero, schnelles Swapping zwischen aktiven Modellen.
*   **Tier 2 (Warm Standby)**: Checkpoint liegt auf **lokaler NVMe SSD**.
    *   *Restore-Zeit*: Sekunden bis wenige Minuten.
    *   *Use-Case*: Überbrückung von längeren Pausen, Freimachen von RAM.
*   **Tier 3 (Cold Storage)**: Checkpoint liegt auf **Network Storage / S3**.
    *   *Restore-Zeit*: Netzwerkabhängig.
    *   *Use-Case*: Disaster Recovery, Langzeit-Archivierung, Migration über Cluster-Grenzen.

### 1.3. Pre-Warmed Snapshots (Templates)
*   **Funktion**: Ein Workload wird gestartet, initialisiert (Gewichte laden, Caches aufbauen, JIT-Kompilierung) und dann gesnapshotet.
*   **Use-Case**: Dieser Snapshot dient als "Gold Image". Neue Replicas starten nicht bei Null ("Cold Start"), sondern aus diesem Zustand.
*   **Effekt**: Eliminierung der typischen 30s - 5min Startzeit von großen LLMs.

## 2. Advanced Scheduling & Orchestration

Diese Features betreffen die Verwaltung des gesamten Clusters und mehrerer Workloads.

### 2.1. Time-Slicing & Resource Reclaiming (Tag/Nacht-Betrieb)
*   **Szenario**: Tagsüber wird Inferenz benötigt (hohe Priorität), nachts Training/Quantisierung (niedrige Priorität).
*   **Ablauf**:
    1.  Inferenz-Last steigt an -> Trainings-Job wird *pausiert* (nicht abgebrochen!).
    2.  Inferenz-Pod übernimmt GPU.
    3.  Inferenz-Last sinkt -> Trainings-Job wird *resumed*.
*   **Business Value**: Maximale Auslastung der teuren GPU-Hardware (24/7 Nutzung).

### 2.2. GPU-Multiplexing (Over-Provisioning)
*   **Konzept**: "Virtual Memory" für GPUs. Wir können mehr Modelle "bereit" halten, als in den VRAM passen.
*   **Beispiel**: Auf einer 80GB A100 können fünf 70B Modelle (je ~40GB) "laufen".
    *   1 Modell ist aktiv im VRAM.
    *   4 Modelle liegen im System-RAM (je 40GB RAM belegt).
    *   Ein Proxy entscheidet millisekundengenau, welches Modell in den VRAM geladen ("geswappt") wird.

### 2.3. Live Migration & Rebalancing
*   **Node Maintenance**: Ein Node muss gepatcht werden. Alle GPU-Workloads werden checkpointed, auf einen anderen Node verschoben und dort resumed.
*   **Spot Instance Evakuierung**: Bei "Termination Warning" (oft nur 30s Vorwarnzeit) wird der State sofort auf Disk/S3 gesichert. Der Job geht nicht verloren.

### 2.4. Pre-Loading (Staging)
*   **Funktion**: Während Modell A auf der GPU rechnet, lädt das System den Checkpoint von Modell B bereits von der SSD in den System-RAM.
*   **Ziel**: Wenn Modell A fertig ist, kann Modell B sofort via PCIe in die GPU geladen werden (Latenz-Minimierung).

## 3. Developer & Operations Experience

### 3.1. "Save Game" für Training
*   **Problem**: Training crasht nach 3 Tagen. Letzter Checkpoint war vor 4 Stunden.
*   **Lösung**: Häufige, inkrementelle RAM-Snapshots. Bei Fehler kann der Zustand von vor 5 Minuten wiederhergestellt werden.

### 3.2. Debugging
*   **Funktion**: Ein fehlerhafter Zustand in der Produktion wird gesnapshotet.
*   **Ablauf**: Der Snapshot wird in eine Dev-Umgebung geklont. Entwickler können den exakten Zustand (Memory, Variablen) inspizieren, der zum Fehler führte.

## 4. Architektur-Implikationen (Vorschau)

Um dieses Feature-Set umzusetzen, benötigen wir:

1.  **Smart Proxy (The "Brain")**: Muss wissen, wo welcher Checkpoint liegt und den Traffic entsprechend routen und Trigger senden.
2.  **Node-Agent (The "Muscle")**: Der Shim/Operator auf dem Node, der  und 
Usage:
  criu dump|pre-dump -t PID [<options>]
  criu restore [<options>]
  criu check [--feature FEAT]
  criu page-server
  criu service [<options>]
  criu dedup
  criu lazy-pages -D DIR [<options>]

Commands:
  dump           checkpoint a process/tree identified by pid
  pre-dump       pre-dump task(s) minimizing their frozen time
  restore        restore a process/tree
  check          checks whether the kernel support is up-to-date
  page-server    launch page server
  service        launch service
  dedup          remove duplicates in memory dump
  cpuinfo dump   writes cpu information into image file
  cpuinfo check  validates cpu information read from image file

Try -h|--help for more info steuert und Speicher (RAM/Disk) verwaltet.
3.  **Storage Fabric**: Ein schnelles, verteiltes Dateisystem oder S3-Integration für Tier 2/3 Storage.
4.  **Scheduler-Integration**: Kubernetes muss wissen, dass ein Pod zwar "da" ist, aber 0 GPU-Ressourcen verbraucht (wenn pausiert).

---
**Nächster Schritt**: Definition der Kubernetes-Komponenten (CRDs, Controller, Proxy), die diese Features abbilden.
