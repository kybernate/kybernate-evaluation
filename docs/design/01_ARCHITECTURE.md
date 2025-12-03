# Architekturvorschlag: Kybernate Shim Operator & Platform

Dieses Dokument beschreibt die Architektur für die **Kybernate**-Plattform. Es erweitert den ursprünglichen Shim-Ansatz um ein umfassendes Feature-Set für GPU-Workload-Management, einschließlich Hierarchical Storage Management (HSM), Multiplexing und Advanced Scheduling.

## 1. Vision & Ziele

Das Ziel ist die Transformation von GPUs von einer statischen Ressource zu einem dynamischen Pool.

1.  **Scale-to-Zero & Instant Resume**: Maximale Effizienz durch Freigabe von GPUs bei Inaktivität und Wiederherstellung im Sekundenbereich.
2.  **GPU Multiplexing (Over-Provisioning)**: Betrieb von mehr Workloads als physischer VRAM vorhanden ist (z.B. 5x 70B Modelle auf einer A100).
3.  **Hierarchical Storage Tiering**: Intelligente Platzierung von Checkpoints (RAM vs. NVMe vs. S3) basierend auf Kosten/Latenz-Anforderungen.
4.  **Workload Mobility**: Nahtlose Migration zwischen Nodes und "Save Games" für lange Trainings-Jobs.

## 2. Architektur-Komponenten

Das System besteht aus Node-Level Komponenten ("Muscle") und Cluster-Level Komponenten ("Brain").

### 2.1. Node-Level (The Muscle)

#### A. Kybernate Shim (Containerd Shim)
Der erweiterte Shim ist die zentrale Steuereinheit pro Container.
*   **Lifecycle Management**: Start, Stop, Pause (Checkpoint), Resume (Restore).
*   **Tiering Logic**: Entscheidet, wohin ein Checkpoint geschrieben wird (RAM, Disk, S3) basierend auf Policies.
*   **Integration**:
    *   `criu`: Für CPU/Prozess-State.
    *   `cuda-checkpoint`: Für VRAM-State.
    *   `runc`: Für die eigentliche Container-Ausführung.

#### B. Node Agent (DaemonSet)
Der Vermittler zwischen der Kubernetes Control Plane und den lokalen Shims.
*   **Kommunikation**: Empfängt Befehle vom Controller (via K8s API/gRPC) und leitet sie an den Shim weiter (via Unix Socket).
*   **Monitoring**: Überwacht VRAM-Nutzung und meldet Metriken an den Controller.

#### C. Activator (Smart Proxy)
Ein Userspace- oder eBPF-basierter Proxy, der vor dem Container sitzt.
*   **Traffic Interception**: Fängt Requests ab, wenn der Container pausiert ist.
*   **Trigger**: Weckt den Shim auf ("Wake-on-LAN" für Container).
*   **Queueing**: Puffert Requests während der Restore-Phase (verhindert Connection Timeouts).
*   **Multiplexing**: Kann Traffic zwischen verschiedenen Modell-Versionen routen (A/B Testing, Blue/Green).

#### D. Local Storage Manager
Verwaltet die lokalen Ressourcen für Checkpoints (logisch Teil des Node Agents/Shims).
*   **Tier 1 (Hot)**: Reserviertes `tmpfs` / Shared Memory für sofortigen Zugriff.
*   **Tier 2 (Warm)**: Lokaler NVMe-Speicher für ausgelagerte Checkpoints.

### 2.2. Cluster-Level (The Brain)

#### E. Kybernate Controller (K8s Operator)
Der Orchestrator des Systems.
*   **Global State**: Weiß, welcher Checkpoint wo liegt (RAM auf Node A, Disk auf Node B, S3).
*   **Scheduling Policies**:
    *   *Time-Slicing*: "Pausiere Training-Job X, weil Inferenz-Request für Y reinkommt."
    *   *Rebalancing*: "Node A ist voll, migriere pausierten Job Z nach Node B."
*   **CRD Management**: Verwaltet `GpuCheckpoint`, `GpuRestoreJob`, `GpuWorkload`.

#### F. Storage Fabric
Abstraktionsschicht für persistenten Speicher.
*   **Tier 3 (Cold)**: S3-kompatibler Object Storage oder Shared Filesystem (Ceph/NFS) für langfristige Speicherung und Migrationen.

## 3. Detaillierte Abläufe & Features

### 3.1. Hierarchical Storage Tiering (HSM)

Der Shim entscheidet beim Suspend dynamisch über das Ziel:

| Tier | Medium | Latenz | Use Case |
| :--- | :--- | :--- | :--- |
| **Tier 0** | **VRAM** | < 1ms | Aktive Inferenz / Training. |
| **Tier 1** | **Sys-RAM** | ~1-2s | "Warm Start", Scale-to-Zero, Multiplexing. |
| **Tier 2** | **NVMe** | ~10-30s | Längere Pausen, RAM freimachen für andere Jobs. |
| **Tier 3** | **S3/Net** | Min. | Migration, Disaster Recovery, "Save Game". |

**Ablauf "Tiering Down" (RAM -> Disk)**:
Wenn der System-RAM knapp wird, kann der Shim einen Checkpoint vom `tmpfs` auf die NVMe verschieben, ohne den Container aufzuwecken.

### 3.2. GPU Multiplexing (Over-Provisioning)

Szenario: 3 große Modelle (A, B, C) auf einer GPU.
1.  **Initial**: Modell A ist aktiv (Tier 0). B und C sind im RAM (Tier 1).
2.  **Request für B**:
    *   Activator empfängt Request für B.
    *   Controller signalisiert Shim A: "Suspend to RAM".
    *   Shim A: `cuda-checkpoint` -> RAM. (A ist nun Tier 1).
    *   Controller signalisiert Shim B: "Resume from RAM".
    *   Shim B: `cuda-checkpoint` restore <- RAM. (B ist nun Tier 0).
    *   Activator leitet Request an B weiter.
3.  **Zeit**: Der Wechsel dauert nur so lange wie der PCIe-Transfer des VRAM-Inhalts (z.B. 2s für 40GB).

### 3.3. Pre-Warmed Snapshots (Templates)

1.  **Template Creation**: Ein "Golden Master" Pod startet, lädt Modellgewichte, kompiliert Shader/JIT.
2.  **Snapshot**: Operator erstellt einen Tier 3 Checkpoint (S3).
3.  **Fast Scale-Out**: Neue Pods starten nicht via `docker run`, sondern via `Restore` aus diesem S3-Checkpoint (direkt in RAM/VRAM).
    *   *Vorteil*: Startzeit sinkt von Minuten auf Sekunden, da Initialisierung übersprungen wird.

## 4. Implementierungsvorschlag

### Phase 1: Foundation (abgeschlossen)
*   **Ziel**: Proof of Concept für GPU-Checkpoint.
*   **Ergebnisse**:
    *   ✅ CRIU CUDA-Plugin funktioniert (2.2 GiB Checkpoint für 2GB VRAM)
    *   ✅ containerd `ctr tasks checkpoint` erstellt valide Checkpoints
    *   ⚠️ Shim-Integration für GPU nicht möglich (BinaryName-Limitation)
    *   ⚠️ CRI CheckpointContainer API nicht in containerd implementiert
    *   🎉 **Two-Stage GPU Checkpoint validiert!** (Durchbruch 2025-12-03)
*   **Two-Stage Checkpoint** (cuda-checkpoint + CRIU):
    1. `cuda-checkpoint --action checkpoint` → VRAM in Host-RAM (Driver API)
    2. `containerd tasks checkpoint` → RAM + CPU auf Disk (CRIU)
    3. Restore umgekehrt: Disk → RAM → VRAM
    *   Prozess ~5 Minuten pausiert, Loop 485 → 490 exakt fortgesetzt
    *   Umgeht alle bekannten CRIU Mount-Bugs!
*   **Architektur-Entscheidung**: GPU-Workloads nutzen `nvidia` RuntimeClass, Checkpoint/Restore über Operator

### Phase 2: Kybernate Operator (nächster Schritt)
*   **Ziel**: Kubernetes-native Checkpoint/Restore ohne manuelle Intervention.
*   **Tech**: Go-basierter Kubernetes Operator mit containerd gRPC Integration.
*   **Warum Operator statt Shim?**:
    *   containerd handhabt Bundle-Setup intern
    *   `runc restore` wird korrekt aufgerufen (umgeht CRIU Mount-Bug)
    *   CRDs ermöglichen deklaratives Management
*   **CRDs**: 
    *   `KybernateCheckpoint` - Checkpoint-Anforderung
    *   `KybernateRestore` - Restore-Anforderung
    *   `KybernateWorkload` - Managed GPU-Workload mit Auto-Suspend

### Phase 3: Storage Fabric & Persistence
*   **Ziel**: Migration und "Save Games".
*   **Tech**: Erweiterung des Operators um S3-Upload/Download.
*   **Storage**: Checkpoint-Images in OCI-Registry oder S3.

### Phase 4: The Brain (Multiplexing)
*   **Ziel**: Over-Provisioning und intelligentes Scheduling.
*   **Tech**: Komplexer K8s Controller, der Metriken (VRAM Usage, PCIe Bandwidth) nutzt, um Entscheidungen zu treffen.
*   **Activator**: Erweiterung um Routing-Logik für mehrere Modelle hinter einer IP.

## 5. Technische Herausforderungen

*   **PCIe Bandwidth**: Der Flaschenhals beim Multiplexing. PCIe 4.0/5.0 ist Pflicht für gute Performance.
*   **Memory Management**: Der Host benötigt sehr viel RAM (mindestens 2-3x VRAM Größe), um effizientes Tiering zu ermöglichen.
*   **Device Mapping**: Konsistente GPU-IDs bei Migrationen sicherstellen (NVIDIA Container Toolkit Konfiguration).

---
**Referenz**: Basiert auf [Zeropod](https://github.com/ctrox/zeropod) und [NVIDIA/cuda-checkpoint](https://github.com/NVIDIA/cuda-checkpoint).
