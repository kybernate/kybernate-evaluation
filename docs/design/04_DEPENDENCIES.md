# Kybernate: System Dependencies & Requirements

Dieses Dokument listet die notwendigen Voraussetzungen (Hardware, Software, Libraries) für die Implementierung und den Betrieb der Kybernate-Plattform auf.

## 1. Hardware-Anforderungen

Da das System stark auf schnellem Datentransfer zwischen VRAM, RAM und Disk basiert, ist die Hardware-Ausstattung kritisch.

### 1.1. GPU & PCIe
*   **NVIDIA GPUs**: Ampere (A100, A10, A40) oder Hopper (H100) Architektur empfohlen. Ältere Karten (Turing/Volta) funktionieren evtl., aber `cuda-checkpoint` Support muss geprüft werden.
*   **PCIe Bandwidth**: PCIe Gen 4.0 oder 5.0 ist **zwingend** für akzeptable Restore-Zeiten beim Multiplexing.
    *   *Ziel*: > 25 GB/s Transfer-Rate Host-to-Device.
*   **VRAM**: Abhängig von den Modellen (z.B. 24GB+ für kleine LLMs, 80GB für große).

### 1.2. Host Memory (RAM)
*   **Kapazität**: Der Host benötigt signifikant mehr RAM als VRAM, um Checkpoints im "Tier 1" (Hot) zu halten.
*   **Faustregel**: `Host RAM >= (Anzahl GPUs * VRAM) * 2`.
    *   *Beispiel*: Node mit 4x A100 (80GB) = 320GB VRAM -> Mindestens 640GB - 1TB Host RAM empfohlen.

### 1.3. Storage
*   **Tier 2 (Warm)**: Lokale NVMe SSDs. Hohe IOPS und sequenzielle Schreibrate (> 3GB/s) für schnelles Auslagern aus dem RAM.
*   **Tier 3 (Cold)**: Schnelle Netzwerkanbindung (10GbE+) zu S3/Ceph für Migrationen.

## 2. Software & OS Level

### 2.1. Betriebssystem & Kernel
*   **Linux Kernel**: Aktueller Kernel (5.15+ oder 6.x) für optimale `userfaultfd` und CRIU Unterstützung.
*   **NVIDIA Treiber**: Proprietäre Treiber (Version 535+ oder neuer), kompatibel mit der verwendeten CUDA-Version.
*   **Cgroups v2**: Muss aktiviert sein für modernes Ressourcen-Management in K8s.

### 2.2. Container Runtime
*   **Containerd**: Version 1.6+ oder 1.7+.
*   **NVIDIA Container Toolkit**: Muss installiert und konfiguriert sein, um GPUs in Container durchzureichen (`nvidia-ctk`).
*   **Runc**: Standard OCI Runtime (wird vom Shim gewrappt).

### 2.3. Kubernetes
*   **Version**: 1.26+ (für stabile CRD APIs und Plugin-Mechanismen).
*   **Runtime Class**: Cluster muss konfiguriert sein, um Custom Runtime Classes (`kybernate`) zu akzeptieren.

## 3. Core Technologies & Libraries

### 3.1. Checkpointing Engines
*   **CRIU (Checkpoint/Restore In Userspace)**:
    *   Muss auf dem Host installiert sein.
    *   Benötigt ggf. Kernel-Patches oder spezifische Capabilities (`CAP_SYS_ADMIN`, `CAP_CHECKPOINT_RESTORE`).
*   **NVIDIA/cuda-checkpoint**:
    *   Die Kern-Bibliothek für das Speichern des VRAMs.
    *   Muss als Library oder Binary für den Shim verfügbar sein.
    *   *Status*: Experimentell, muss ggf. aus Source kompiliert werden.

### 3.2. Development Stack (für die Entwicklung von Kybernate)
*   **Sprache**: Go (Golang) 1.21+ für Controller, Shim und Agent.
*   **gRPC / Protobuf**: Für die interne Kommunikation (Agent <-> Shim).
*   **Kubebuilder / Operator SDK**: Frameworks zum Erstellen des K8s Controllers.
*   **Containerd Shim V2 API**: Go-Bindings zur Entwicklung des Custom Shims.

## 4. Netzwerkanforderungen

*   **Inter-Node**: Hohe Bandbreite für Live-Migrationen von Checkpoints.
*   **Localhost**: Unix Domain Sockets für latenzfreie Kommunikation zwischen Agent und Shim.

## 5. Zusammenfassung der Critical Path Dependencies

1.  **`cuda-checkpoint` Stabilität**: Das gesamte Projekt steht und fällt mit der Zuverlässigkeit dieses Tools.
2.  **PCIe Flaschenhals**: Hardware muss schnell genug sein, sonst ist "Instant Resume" nicht "Instant".
3.  **CRIU Komplexität**: TCP-Verbindungen wiederherzustellen ist schwierig; der "Activator" Proxy ist hierfür eine notwendige Abhängigkeit, um dieses Problem zu umgehen (statt es in CRIU zu lösen).
