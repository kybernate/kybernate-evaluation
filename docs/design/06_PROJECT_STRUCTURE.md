# Projektstruktur & Installations-Mechanismus

Dieses Dokument beschreibt die Code-Organisation des Kybernate-Projekts. Die Struktur folgt gängigen Go-Standards für Kubernetes-Operatoren und System-Addons.

Ziel ist es, dass das gesamte System über ein einziges `kubectl apply` (oder Helm Chart) installiert werden kann, ohne dass manuelle Eingriffe auf den Nodes nötig sind.

## 1. Verzeichnisstruktur

```text
KYBERNATE/
├── cmd/                                # Entrypoints für die ausführbaren Binaries
│   ├── containerd-shim-kybernate-v1/   # Der Shim selbst. Name ist Konvention (v2 API).
│   │   └── main.go
│   ├── node-agent/                     # Der DaemonSet Prozess pro Node.
│   │   └── main.go
│   ├── controller/                     # Der zentrale K8s Operator (Deployment).
│   │   └── main.go
│   └── installer/                      # Hilfs-Tool, das im Init-Container läuft.
│       └── main.go
├── pkg/                                # Wiederverwendbarer Bibliotheks-Code
│   ├── apis/                           # K8s CRD Definitionen (Go Structs & DeepCopy).
│   ├── shim/                           # Die Kern-Logik des Shims (Lifecycle).
│   ├── criu/                           # Wrapper für CRIU und cuda-checkpoint Befehle.
│   ├── hsm/                            # Hierarchical Storage Manager (Tiering Logik).
│   │   ├── storage.go                  # Interface für Storage Backends.
│   │   ├── s3.go                       # S3 Implementierung.
│   │   └── local.go                    # NVMe/RAM Implementierung.
│   └── config/                         # Konfigurations-Handling (Flags, Env Vars).
├── deploy/                             # Kubernetes Manifeste für die Installation
│   ├── crds/                           # GpuCheckpoint, GpuWorkload CRDs.
│   ├── daemonset.yaml                  # Installiert Agent & Shim auf Nodes.
│   ├── runtimeclass.yaml               # Registriert 'kybernate'.
│   └── rbac.yaml                       # ServiceAccounts und Rollen.
├── build/                              # Dockerfiles & Build-Skripte
│   ├── Dockerfile.shim-installer       # Image, das den Shim auf den Host kopiert.
│   ├── Dockerfile.node-agent           # Image für den Node Agent.
│   └── Dockerfile.controller           # Image für den Controller.
├── go.mod                              # Go Module Definition.
└── Makefile                            # Build-Automatisierung (build, push, deploy).
```

## 2. Der Installations-Mechanismus (Self-Installing Addon)

Wir nutzen das **DaemonSet-Pattern mit Init-Containern**, um Systemkomponenten auf dem Host zu installieren, ähnlich wie der NVIDIA GPU Operator oder CNI-Plugins.

### 2.1. Ablauf beim Deployment

1.  **Init-Container (`installer`)**:
    *   Startet privilegiert auf jedem Node.
    *   Mountet Host-Pfade:
        *   `/host/usr/local/bin` (Ziel für Shim Binary).
        *   `/host/etc/containerd` (Ziel für Config Patch).
    *   **Aktion 1**: Kopiert das kompilierte `containerd-shim-kybernate-v1` Binary aus dem Container-Image auf den Host.
    *   **Aktion 2**: Erkennt die Umgebung (MicroK8s vs. Vanilla) und patcht die `config.toml` von containerd, um die Runtime `kybernate` zu registrieren.
    *   **Aktion 3**: Sendet `SIGHUP` an den containerd-Prozess, damit die Config neu geladen wird.

2.  **Main-Container (`node-agent`)**:
    *   Startet nach erfolgreichem Init.
    *   Mountet den Kommunikations-Socket (z.B. `/run/kybernate/shim.sock`).
    *   Übernimmt die Steuerung und das Monitoring (HSM, Metriken).

3.  **RuntimeClass**:
    *   Ein separates Manifest legt die `RuntimeClass` an, die auf den Handler `kybernate` verweist.

### 2.2. Vorteile
*   **Kein manuelles SSH**: Der Admin muss sich nicht auf den Nodes einloggen.
*   **Atomare Updates**: Ein Update des DaemonSet-Images aktualisiert automatisch das Shim-Binary auf allen Nodes.
*   **Portabilität**: Der Installer enthält die Logik, um Pfad-Unterschiede (MicroK8s Snap Pfade) zur Laufzeit zu behandeln.
