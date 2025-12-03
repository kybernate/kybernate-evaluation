# Kybernate

**Kybernate** ist eine Kubernetes-native Plattform für fortschrittliches GPU-Workload-Management. Sie transformiert statische GPU-Ressourcen in einen dynamischen Pool durch Technologien wie Checkpoint/Restore, Hierarchical Storage Management (HSM) und Multiplexing.

## Projektübersicht

Das Ziel von Kybernate ist es, die Effizienz von GPU-Clustern drastisch zu steigern, indem es "Scale-to-Zero", "Instant Resume" und "Over-Provisioning" für AI/ML-Workloads ermöglicht.

### Kern-Features
*   **Scale-to-Zero**: Freigabe von GPUs bei Inaktivität.
*   **Instant Resume**: Wiederherstellung von Workloads aus dem RAM (< 1s) oder NVMe.
*   **Multiplexing**: Ausführung von mehr Modellen als physischer VRAM vorhanden ist.
*   **HSM**: Intelligentes Tiering von Checkpoints (VRAM -> RAM -> NVMe -> S3).

## Dokumentation

Die Dokumentation ist in zwei Bereiche unterteilt:

### 1. Design & Architektur (`docs/design/`)
Hier finden sich die Konzepte, Architekturentscheidungen und Spezifikationen.

*   [**01_ARCHITECTURE.md**](docs/design/01_ARCHITECTURE.md): High-Level Architektur, Komponenten (Shim, Agent, Controller) und Vision.
*   [**02_FEATURE_SET.md**](docs/design/02_FEATURE_SET.md): Detaillierte Beschreibung der Features (HSM, Multiplexing, Pre-warming).
*   [**03_K8S_COMPONENTS.md**](docs/design/03_K8S_COMPONENTS.md): Technische Spezifikation der CRDs (`GpuCheckpoint`, `GpuWorkload`) und APIs.
*   [**04_DEPENDENCIES.md**](docs/design/04_DEPENDENCIES.md): Liste der Hardware- und Software-Anforderungen (PCIe 4.0, CRIU, cuda-checkpoint).
*   [**05_MICROK8S_STRATEGY.md**](docs/design/05_MICROK8S_STRATEGY.md): Strategie für die Entwicklung auf MicroK8s und Portierung auf Standard-K8s.

### 2. Setup & Installation (`docs/setup/`)
Schritt-für-Schritt-Anleitungen zum Aufsetzen der Entwicklungsumgebung auf Ubuntu 24.04.

*   [**01_INSTALL_DRIVER.md**](docs/setup/01_INSTALL_DRIVER.md): Installation von Docker, NVIDIA Treibern, CUDA Toolkit 13, Container Toolkit, `cuda-checkpoint` und CRIU.
*   [**02_INSTALL_MICROK8S.md**](docs/setup/02_INSTALL_MICROK8S.md): Setup von MicroK8s, Aktivierung von Addons (GPU, DNS, Dashboard) und Konfiguration von `crictl`.

## Schnellstart

Um eine Entwicklungsumgebung aufzusetzen, folgen Sie bitte den Anleitungen im Ordner `docs/setup/` in der nummerierten Reihenfolge.

1.  Bereiten Sie den Host vor (Treiber, CRIU, Tools): `docs/setup/01_INSTALL_DRIVER.md`
2.  Installieren Sie Kubernetes (MicroK8s): `docs/setup/02_INSTALL_MICROK8S.md`
