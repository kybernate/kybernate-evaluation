# Architektur

Ziel: GPU-Workloads unter MicroK8s/Containerd checkpointen/restoren mit hierarchischem Storage-Tiering (VRAM → RAM → NVMe → S3) und kurzer Resume-Zeit. CUDA-Checkpointing erfolgt über die CUDA Checkpoint API (nicht das Binary). Vor dem CRIU-Checkpoint wird der CUDA-Checkpoint ausgeführt, um VRAM nach RAM zu schieben und den Workload zu pausieren.

Service Level Orientierungen (Richtwerte)
- Resume aus Tier 1 (RAM): <1 s
- Resume aus Tier 2 (NVMe): wenige Sekunden
- Resume aus Tier 3 (S3/Netz): abhängig von Artefaktgröße, via Prefetch minimiert
- Checkpoint-Dauer: abhängig vom VRAM/CPU-Footprint; CUDA-Checkpoint muss vor CRIU abgeschlossen sein

## Komponenten

- Containerd Shim (GPU-aware)
	- Interzeptiert Lifecycle-Hooks (pause/stop) und triggert GPU-spezifische Pre-/Post-Checkpoint Hooks.
	- Koordiniert mit dem Node-Agent den Übergang in einen konsistenten Zustand (CUDA-Checkpoint vor CRIU).

- Node-Agent (DaemonSet)
	- Läuft auf jedem GPU-Knoten; stellt einen lokalen gRPC-Service bereit.
	- Führt CUDA Checkpoint API aus: pausiert/lockt den CUDA-Kontext, schiebt VRAM-Inhalte in RAM, erzeugt GPU-spezifische Artifacts.
	- Startet anschließend den CRIU-Checkpoint-Prozess für den Container/Pod, sobald der CUDA-Checkpoint abgeschlossen ist.
	- Handhabt Restore: staged Artifacts laden (S3/NVMe → RAM → VRAM) und Prozess fortsetzen.
	- Unterstützt Rebalancing: Checkpoint auf Node/GPU A, Restore auf Node/GPU B (in Abstimmung mit Operator/Device Plugin).

- Operator/Controller
	- Deklariert Policies: Tiering (VRAM/RAM/NVMe/S3), Scale-to-Zero, Preemption, Multiplexing, Rebalancing.
	- Orchestriert Checkpoints und Restores über Custom Resources; plant, wann welcher Pod in welches Tier verschoben wird.
	- Steuert, wann Prefetch/Promotion erfolgt (z.B. Cold → Warm → Hot), um Instant Resume zu ermöglichen.
	- Verwaltet Rebalancing zwischen GPU-Gruppen/Nodes (Pause + Checkpoint → Restore auf anderem Ziel).
	- Potenzielle CRDs: `CheckpointPolicy`, `CheckpointRequest`, `RestoreRequest`, `TierPlacement`, `RebalanceRequest`.

- Sidecar/Checkpoint Helper
	- Verpackt/streamt Artifacts (CUDA + CRIU Dumps) in die konfigurierten Storage-Tiers.
	- Unterstützt Prefetch (z.B. NVMe → RAM) und optionales Inline-Dekomprimieren.
	- Kann im Restore-Fall gezielt nur benötigte Segmente laden (Lazy/On-Demand), sofern Workload das zulässt.

- Storage Tier Manager (optional eigene Komponente oder Teil des Operators)
	- HSM-ähnliche Entscheidungen: wann Artefakte von Tier 3 (S3) nach Tier 2 (NVMe) oder Tier 1 (RAM) promotet werden.
	- Trackt LRU/Hotness für GPU-Modelle (z.B. vLLM Shards) und orchestriert Hintergrundkopien.
	- Unterstützt Cross-Node-Rebalancing durch gezielte Platzierung der warmen/cold Artefakte.

- Metadata/Registry Service
	- Speichert Checkpoint-Metadaten (Commit-ID, Model-Version, Timestamps, Tier-Ort, Hashes, Ziel-GPU-Group/Node).
	- Dient dem Operator für Auswahl, Konsistenzprüfungen und Rebalance-Entscheidungen.

- Device Plugin (GPU-Groups/Seats)
	- Liefert logische Ressourcen (`kyb.ai/gpu-1x-seat`, `kyb.ai/gpu-2x-seat`, ...) gemäß Design `docs/design/08_GPU_DEVICE_PLUGIN_ARCHITECTURE.md`.
	- Unterstützt Overprovisioning, Multi-GPU-Modelle, GPU-Groups und Seats.
	- Stellt dem Pod GPU-Indizes/Group/Seat-Infos bereit; arbeitet mit Operator zusammen für Active-Seat-Steuerung.

### Prozesse pro Komponente

- Device Plugin
	- Discover Topologie, bildet GPU-Groups und Seats, exportiert Ressourcen.
	- `ListAndWatch` hält Seat-Health aktuell; `Allocate` übergibt GPU-IDs/Group/Seat an den Pod.
	- Arbeitet mit Operator für Active-Seat-Steuerung zusammen (Multiplexing/Scale-to-Zero/Rebalance).

- Containerd Shim
	- Pre-Hook: I/O quiesce, Streams sync, neue GPU-Work blocken; signalisiert Agent „CUDA checkpoint jetzt“.
	- Post-Hook: Nach CRIU-Restore Pause aufheben.

- Node-Agent
	- CUDA Checkpoint API: lockt Kontext, schiebt VRAM→RAM, erzeugt GPU-Dump.
	- Startet CRIU-Dump nach erfolgreichem CUDA-Checkpoint; orchestriert Restore.
	- Rebalancing: Checkpoint auf Node/GPU A, Restore auf Node/GPU B.

- Sidecar/Checkpoint Helper
	- Bündelt CUDA+CRIU Artefakte, schreibt in Tier 1/2/3, optional Kompression/Chunking.
	- Prefetch/Promotion (S3→NVMe→RAM), optional Lazy-Load.

- Storage Tier Manager
	- HSM-Logik (Hotness/LRU/SLO), Promotion/De-Promotion, Platzierung für Rebalance.

- Metadata/Registry
	- Hash/Ort/Timestamp/GPU-Group/Node/Version; Grundlage für Auswahl und Konsistenz.

- Operator
	- CRDs: `CheckpointPolicy`, `CheckpointRequest`, `RestoreRequest`, `TierPlacement`, `RebalanceRequest`.
	- Policies für Tiering, Preemption, Multiplexing, Scale-to-Zero, Rebalancing; triggert Prefetch/Promotion.

### Komponentenübersicht (ASCII)

```
+-------------------+        +------------------+        +---------------------+
|   Operator/CRDs   | <----> |  Metadata/Reg.   | <----> | Storage Tier Manager|
+---------+---------+        +--------+---------+        +----------+----------+
		  |                           |                             |
		  | reconcile                 | lookup/update               |
		  v                           |                             v
+---------+---------+        +--------+---------+        +----------+----------+
|  Device Plugin    | -----> | Containerd Shim  | <----> |  Sidecar/Helper     |
| (GPU-Groups/Seats)|        | (GPU-aware)      |        | (pack/prefetch)     |
+---------+---------+        +--------+---------+        +----------+----------+
		  | allocate                   | hooks                        |
		  v                            v                              |
+---------+---------+        +--------+---------+                     |
|     Pod/Container | <----> |   Node-Agent     | <-------------------+
| (App + CUDA ctx)  |        | (CUDA API + CRIU)|    artifacts (Tier1/2/3)
+-------------------+        +------------------+
```

## Checkpoint-Flow (Happy Path)
1) Trigger (Policy, Timer, Preemption, Idle): Operator signalisiert dem Shim/Node-Agent.
2) Pre-Flight: Shim/Agent quiesziert I/O, stoppt neue GPU-Work, synchronisiert Streams.
3) CUDA-Checkpoint (API): Agent paust/lockt den CUDA-Kontext, verschiebt VRAM-Inhalte nach RAM und erzeugt GPU-Artefakte.
4) CRIU-Checkpoint: Nachdem CUDA abgeschlossen ist, läuft CRIU für den Prozess/Container (GPU-Zustand liegt bereits im RAM). CPU/Memory/FDs/Namespaces werden gesichert.
5) Packaging & Tiering: Sidecar/Helper bündelt CUDA+CRIU Dumps, legt sie gemäß Policy in Tier 1/2/3 ab; Hash/Metadata wird registriert.
6) Optional: Hintergrund-Promotion/De-Promotion zwischen Tiers (HSM-Flow).

ASCII-Skizze Checkpoint

```
Trigger -> [Shim] --pre-hook--> [Node-Agent]
				| CUDA API: VRAM->RAM, lock
				v
			[CRIU checkpoint]
				v
		 [Sidecar pack & push]
			  |        \
		    Tier1/2      Tier3(S3)
```

## Restore-Flow (Happy Path)
1) Request/Traffic oder Schedule: Operator wählt einen Checkpoint und Zielknoten.
2) Prefetch: Storage Manager/Sidecar zieht Artefakte aus Tier 3→2→1 nach Bedarf (z.B. NVMe → RAM für Instant Resume). Bei Rebalancing: Prefetch auf Ziel-Node.
3) Stage GPU-Artifacts: Agent lädt CUDA-Artefakte; bei Bedarf VRAM-Warmup für Tier 0.
4) CRIU-Restore: Prozess/Container wird mit CRIU gestartet; GPU-Kontext liegt bereit.
5) Resume: Shim hebt Pause auf, Workload bedient Requests. Optional Lazy/On-Demand-Rehydrierung weiterer Blöcke.

ASCII-Skizze Restore

```
Tier3/S3 -> Tier2/NVMe -> Tier1/RAM
			  |             |
			  v             v
		  [Sidecar] --> [Node-Agent]
						| CRIU restore
						v
					[Shim resume] -> Pod läuft
```

## Multiplexing / Scale-to-Zero
- Multiplexing: Mehr Modelle als VRAM, durch sequenzielles Aktivieren. Operator entscheidet, welches Modell in VRAM (Tier 0) geladen wird; andere bleiben in RAM/NVMe/S3.
- Scale-to-Zero: Bei Idle wird Pod pausiert und Tier heruntergestuft (VRAM→RAM→NVMe→S3). Bei Traffic wird promotet und restored.
- Rebalancing: Operator kann Pods zwischen GPU-Groups/Nodes verschieben (Checkpoint auf Quelle, Restore auf Ziel), abgestimmt mit Device Plugin/Seats.

ASCII-Skizze Multiplexing/Rebalancing

```
[Group G01]
	Seat A (active, VRAM)
	Seat B (parked, RAM/NVMe)
	Seat C (cold, S3)

Traffic to B -> Operator sets B active:
	checkpoint A -> demote
	prefetch B -> restore -> promote to VRAM

Rebalance:
	[Node A] AgentA --checkpoint--> Tier3/S3 --prefetch--> Tier2_B/Tier1_B --restore--> [Node B] AgentB -> ShimB resume
```

## Sicherheit und Integrität
- Hash/Checksum pro Artifact; Signaturen optional.
- Least-privilege: Agent läuft lokal, Operator nur Steuerpfad; Containerd-Shim minimiert Angriffsfläche.
- Isolation: CRIU und CUDA-Checkpoint laufen im Kontext des Pods; Artefaktzugriff über ServiceAccount/Secrets.

## Beobachtbarkeit
- Metriken: Checkpoint-Dauer, Restore-Dauer pro Tier, Promotion/De-Promotion-Zähler, Fehlerraten.
- Events: Kubernetes Events/CR-Status für Operator; Logs im Agent/Sidecar/Shim.

## Grenzen / Annahmen
- NVIDIA GPU, CUDA Checkpoint API verfügbar; Kernel/CRIU kompatibel.
- Netzwerkpfad zu Tier 3 (S3/Share) ausreichend schnell für Cold → Warm.
- Prefetch-Strategien sind workloadabhängig und werden per Policy konfiguriert.
