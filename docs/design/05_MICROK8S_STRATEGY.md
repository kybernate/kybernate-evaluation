# Strategie: Entwicklung auf MicroK8s & Portierung auf Standard-K8s

Dieses Dokument beschreibt die Strategie, um die Kybernate-Plattform primär auf MicroK8s zu entwickeln und sicherzustellen, dass sie nahtlos auf Standard-Kubernetes-Cluster (Vanilla, EKS, GKE, Bare-Metal) portierbar ist.

## 1. Entwicklungsplattform: MicroK8s

MicroK8s dient als primäre Entwicklungsumgebung. Es bietet eine zertifizierte Kubernetes-Distribution, die ideal für schnelle Iterationszyklen auf lokalen Workstations ist.

### 1.1. Vorteile für die Entwicklung
*   **Schnelle Iteration**: Single-Node Cluster ermöglichen direktes Testen ohne komplexe CI/CD-Pipelines. Images können direkt in den lokalen Cache geladen werden.
*   **Integrierter GPU-Support**: Das `gpu`-Addon automatisiert die Installation von NVIDIA-Treibern und dem Container Toolkit, was die Rüstzeiten für Entwickler minimiert.
*   **Versionierung**: Einfaches Testen verschiedener Kubernetes-Versionen durch Snap-Channels.

## 2. Technische Abstraktion & Portabilität

Die größte Herausforderung bei der Nutzung von MicroK8s als Basis für allgemeine K8s-Software liegt in der Snap-Isolierung und den abweichenden Dateipfaden. Die Architektur von Kybernate muss diese Unterschiede abstrahieren.

### 2.1. Pfad-Abstraktion (Snap vs. Standard)
MicroK8s verwendet nicht-standardkonforme Pfade für Containerd-Sockets und Konfigurationsdateien.

| Komponente | Standard K8s Pfad | MicroK8s Pfad |
| :--- | :--- | :--- |
| **Containerd Config** | `/etc/containerd/config.toml` | `/var/snap/microk8s/current/args/containerd-template.toml` |
| **Containerd Socket** | `/run/containerd/containerd.sock` | `/var/snap/microk8s/common/run/containerd.sock` |
| **CNI Plugins** | `/opt/cni/bin` | `/var/snap/microk8s/current/opt/cni/bin` |
| **Runc Binary** | `/usr/bin/runc` | `/var/snap/microk8s/current/bin/runc` |

**Strategie**:
*   **Konfigurierbarkeit**: Der **Node Agent** und der **Shim** dürfen keine hartkodierten Pfade enthalten. Alle Pfade müssen über CLI-Flags oder Environment-Variablen injizierbar sein.
*   **Installations-Logik**: Ein intelligentes Setup-Skript (`install_shim.sh`) erkennt die Umgebung zur Laufzeit und setzt die korrekten Pfade automatisch.

### 2.2. RuntimeClass als Schnittstelle
Um die Portabilität auf Workload-Ebene zu gewährleisten, nutzen wir Kubernetes-native Abstraktionen.

Wir definieren eine `RuntimeClass` namens `kybernate`:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kybernate
handler: kybernate # Verweist auf den Eintrag in der containerd config
```

*   **Implementierung**: Die Verknüpfung des Handlers `kybernate` mit dem tatsächlichen Shim-Binary erfolgt in der `containerd`-Konfiguration des jeweiligen Nodes.
*   **Portabilität**: Der Endanwender nutzt in seinen Pods lediglich `runtimeClassName: kybernate`. Ihm ist die zugrundeliegende Distribution (MicroK8s oder Vanilla) verborgen.

### 2.3. Privilegien & AppArmor
Da `criu` und `cuda-checkpoint` tiefgreifende Systemzugriffe benötigen (ptrace, Memory Injection), müssen Sicherheitsrichtlinien beachtet werden.
*   **Entwicklung**: Nutzung von `privileged` Containern und ggf. Anpassung der AppArmor-Profile innerhalb des MicroK8s Snaps.
*   **Produktion**: Definition minimaler Capabilities (`CAP_SYS_ADMIN`, `CAP_CHECKPOINT_RESTORE`), um die Sicherheit auf Standard-Clustern zu gewährleisten.

## 3. Roadmap: Von Dev zu Prod

Der Übergang von der lokalen Entwicklung zur Produktion erfolgt in drei Phasen.

### Phase 1: Entwicklung (MicroK8s)
*   **Umgebung**: Lokale Workstation mit GPU.
*   **Fokus**: Funktionalität von Shim, Agent und Controller.
*   **Deployment**: Manuelles Patchen der MicroK8s-Configs und Nutzung lokaler Pfade.

### Phase 2: Validierung (Standard K8s)
*   **Umgebung**: Bare-Metal Cluster (z.B. installiert via `kubeadm` auf Ubuntu Server).
*   **Fokus**: Validierung der Pfad-Abstraktion und des Installers.
*   **Test**: Live-Migration von Checkpoints über das Netzwerk zwischen echten Nodes.

### Phase 3: Produktion (Managed K8s)
*   **Umgebung**: EKS, GKE, AKS oder große On-Premise Cluster.
*   **Fokus**: Skalierbarkeit und Integration.
*   **Herausforderung**: Eingeschränkter Zugriff auf Node-Konfigurationen.
*   **Lösung**: Nutzung von DaemonSet-basierten Installern (ähnlich dem NVIDIA GPU Operator), die temporär privilegierte Pods nutzen, um den Host zu konfigurieren, falls kein direkter SSH-Zugriff besteht.

## 4. Fazit

Die Entwicklung auf MicroK8s ist eine valide und effiziente Strategie. Durch die strikte Trennung von Konfiguration (Pfade) und Logik (Code) sowie die Nutzung von Standard-APIs (RuntimeClass, Containerd Shim v2) wird sichergestellt, dass Kybernate ohne Code-Änderungen auf jede Kubernetes-Distribution portiert werden kann.
