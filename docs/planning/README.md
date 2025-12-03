# Kybernate Development Roadmap

Dieses Verzeichnis enthält die detaillierte Planung der Entwicklungsphasen. Jede Phase ist in einzelne Tasks unterteilt, die als separate Markdown-Dateien abgelegt werden.

## Phasen-Übersicht

### Phase 1: Foundation & PoC (Proof of Concept)
**Ziel**: Ein manueller "Scale-to-Zero" Durchstich auf einem einzelnen Node.
*   [**TASK-01_ENV_SETUP.md**](TASK-01_ENV_SETUP.md): Aufsetzen der Entwicklungsumgebung und Verifikation der Tools (`criu`, `cuda-checkpoint`).
*   [**TASK-02_MANUAL_CHECKPOINT.md**](TASK-02_MANUAL_CHECKPOINT.md): Manuelles Checkpointing eines GPU-Prozesses (ohne K8s) zur Validierung der Machbarkeit.
*   [**TASK-03_K8S_GPU_CHECKPOINT.md**](TASK-03_K8S_GPU_CHECKPOINT.md): K8s-Pods mit GPU über `crictl`/`criu` sichern und wiederherstellen (ohne Shim).
*   [**TASK-04_SHIM_SKELETON.md**](TASK-04_SHIM_SKELETON.md): Erstellung des Containerd Shim Gerüsts (Go) und Integration in MicroK8s.
*   [**TASK-05_SHIM_INTEGRATION.md**](TASK-05_SHIM_INTEGRATION.md): Implementierung der Checkpoint/Restore Logik im Shim (CPU).
*   [**TASK-06_GPU_CHECKPOINT.md**](TASK-06_GPU_CHECKPOINT.md): Erweiterung des Shims um GPU-Support (CUDA Checkpoint).

### Phase 2: Kubernetes Integration (The Muscle)
**Ziel**: Steuerung über Kubernetes CRDs und Node Agent.
*   [**TASK-07_CRD_CONTROLLER.md**](TASK-07_CRD_CONTROLLER.md): Definition der CRDs (`GpuCheckpoint`) und Basis-Controller Logik.
*   [**TASK-08_NODE_AGENT.md**](TASK-08_NODE_AGENT.md): Implementierung des Node Agents und gRPC Kommunikation zum Shim.

### Phase 3: Advanced Features (The Brain)
**Ziel**: Automatisierung, HSM und Multiplexing.
*   [**TASK-09_ACTIVATOR_PROXY.md**](TASK-09_ACTIVATOR_PROXY.md): Bau des "Smart Proxy" für Request-Buffering.
*   [**TASK-10_HSM_TIERING.md**](TASK-10_HSM_TIERING.md): Implementierung der Storage-Logik (RAM <-> Disk <-> S3).
*   [**TASK-11_SCHEDULER.md**](TASK-11_SCHEDULER.md): Multiplexing-Logik im Controller.

## Workflow

1.  Wähle den nächsten Task aus der Liste.
2.  Lies die detaillierte Beschreibung in der entsprechenden `.md` Datei.
3.  Implementiere die Lösung.
4.  Markiere den Task als "Completed" und aktualisiere ggf. die Dokumentation.
