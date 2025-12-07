# Checkpoint Artifact Layout

Dieses Dokument beschreibt die physische Struktur eines Checkpoints auf dem Speichermedium (NVMe, S3, Tarball). Eine strikte Struktur erleichtert Debugging, Versionierung und die Entwicklung unabhängiger Tools (z.B. Sidecar).

## Verzeichnisstruktur

Ein Checkpoint-Verzeichnis (oder entpackter Tarball) hat folgende Struktur:

```text
/checkpoint-<ID>/
├── metadata.json          # Globales Manifest (Version, Timestamp, Hashes)
├── cuda/
│   ├── context.json       # CUDA Context State (Allocations, Streams)
│   ├── vram-0.bin         # Raw Memory Dump von GPU 0
│   └── vram-1.bin         # Raw Memory Dump von GPU 1 (bei Multi-GPU)
├── criu/
│   ├── inventory.img      # CRIU Inventory
│   ├── core-*.img         # CPU Register & State
│   ├── mm-*.img           # Main Memory Pages
│   └── ...                # Weitere CRIU Images
├── fs/
│   └── rootfs-diff.tar    # Dateisystem-Änderungen (OverlayFS Diff, optional)
└── logs/
    ├── agent.log          # Logs des Checkpoint-Vorgangs
    └── criu.log           # CRIU Debug Logs (wichtig für Fehleranalyse)
```

## Metadata Manifest (`metadata.json`)

Das Manifest ist die "Source of Truth" für den Checkpoint.

```json
{
  "version": "v1",
  "id": "ckpt-12345",
  "created_at": "2025-12-07T10:00:00Z",
  "components": {
    "cuda": { 
      "size": 16106127360, 
      "checksum": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "files": ["cuda/context.json", "cuda/vram-0.bin"]
    },
    "criu": { 
      "size": 2048576, 
      "checksum": "sha256:..." 
    }
  },
  "topology": {
    "gpu_group": "G01",
    "seats_required": 2,
    "driver_version": "535.129.03",
    "cuda_version": "12.2"
  },
  "source": {
    "pod_name": "vllm-worker-0",
    "namespace": "default",
    "node": "gpu-node-1"
  }
}
```
