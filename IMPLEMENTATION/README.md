# Kybernate – Neustart

Zielsystem: GPU-fähige Container-Workloads in MicroK8s/Containerd checkpointen, pausieren und aus Snapshots wiederherstellen. Fokus auf geringe Downtime, skalierbare GPU-Nutzung und schnelles Wiederaufsetzen von langlaufenden Trainings-, Quantisierungs- oder Inference-Jobs (z.B. vLLM).

Kernziele
- Unterbrechbare/fortsetzbare CUDA-Workloads: Checkpoint/Restore ohne Neu-Start langer Phasen.
- Storage-Tiering für Checkpoints: VRAM (Tier 0) → RAM (Tier 1, hot standby) → NVMe/SSD (Tier 2, warm) → Netzwerk/S3 (Tier 3, cold) mit HSM-Flow.
- Scale-to-Zero & Multiplexing: GPUs bei Inaktivität freigeben; mehr Modelle betreiben als VRAM vorhanden, durch bedarfsweises Restore.
- Instant Resume: Schnelles Wiedereinsteigen (<1s aus RAM, wenige Sekunden aus NVMe) und Quick Load von vLLM/Inference-Snapshots.
- Resilienz: Weiterlaufen nach Preemption/HW-Failure ohne erneutes Training.
- Rebalancing: Workloads checkpointen/pausieren auf GPU/Node A und auf GPU/Node B wiederherstellen.

Service Level Orientierungen (Richtwerte)
- Resume aus Tier 1 (RAM): <1 s
- Resume aus Tier 2 (NVMe): wenige Sekunden
- Resume aus Tier 3 (S3/Netz): abhängig vom Artefakt-Volumen, optimiert durch Prefetch
- Checkpoint-Dauer: abhängig vom VRAM/CPU-Footprint; CUDA-Checkpoint muss vor CRIU abgeschlossen sein.

Hauptkomponenten
- Containerd-Shim-Erweiterung für GPU-Checkpoint/Restore (Pre-/Post-Hooks, koordiniert CUDA vor CRIU).
- Node-Dienst (DaemonSet) für CUDA-Checkpoint via API und CRIU-Integration, mit Rebalancing-Unterstützung.
- Operator/Controller für Policies (Tiering, Preemption, Scale-to-Zero, Multiplexing, Rebalancing), Prefetch/Promotion und CRDs (`CheckpointPolicy`, `CheckpointRequest`, `RestoreRequest`, `RebalanceRequest`, `TierPlacement`).
- Sidecar/Helpers für Packaging, Upload/Download, Prefetch und Lazy-Load.
- Eigenes Device Plugin für GPU-Groups/Seats (Overprovisioning, Multi-GPU, Rebalancing) gemäß `docs/design/08_GPU_DEVICE_PLUGIN_ARCHITECTURE.md`.
- Storage Tier Manager für HSM-Promotion/De-Promotion (S3→NVMe→RAM→VRAM) und platzierungsbewusstes Rebalancing.
- Metadata/Registry Service als separater, ausfallsicherer Persistence-Dienst (nicht K8s-Control-Plane-etcd) mit Rebuild-Möglichkeit aus Artefakten.

Hinweise zur Installation: Siehe `INSTALLATION.md`.
