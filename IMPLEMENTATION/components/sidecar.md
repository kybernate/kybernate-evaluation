# Sidecar / Checkpoint Helper

## Zweck
- Paketiert CUDA- und CRIU-Dumps, schreibt sie in die konfigurierten Storage-Tiers (RAM/NVMe/S3).
- Führt Prefetch/Promotion aus (S3→NVMe→RAM) und optional Dekomprimierung.
- Unterstützt Lazy/On-Demand Loading einzelner Segmente.

## Verantwortlichkeiten
- Packaging: Tar/Compress/Chunk der Artefakte; Checksums generieren.
- Upload/Download: Tier-abhängig (lokal vs. Remote/S3), Resumable Transfers wo möglich.
- Prefetch: pro Restore/Rebalance vorwärmen; kann vom Operator/Tier Manager angestoßen werden.
- Metadata-Update: Pfade/Hashes an Registry melden.

## Schnittstellen
- IPC/Filesystem mit Node-Agent (Artefaktverzeichnis).
- Storage APIs: lokales FS, NVMe-Pfad, S3/Objektstore.
- Control: gRPC/HTTP-Trigger vom Operator/Tier Manager oder Sidecar-internal CLI.

## Platzierung im Monorepo
- Code: `pkg/checkpoint/sidecar/` (Go) mit Storage-Backends unter `pkg/checkpoint/storage/{s3,nvme,ram}/`.
- Deployment: als Sidecar-Container in Pods, evtl. auch als Helper-Job für Offload.
- Manifeste: `manifests/sidecar/` Beispiele.

## Implementierungshinweise
- Stream-orientiertes Packaging (Pipes) um RAM-Footprint zu minimieren.
- Checksums pro Chunk; optional Signaturen.
- Konfigurierbare Kompression (lz4/zstd) und Parallelität.
- Rate-Limits und Retries bei Remote-Stores.

## Security
- Minimal benötigte Credentials für S3/Share; Mount-Scopes eng fassen.
- Keine unnötigen Host-Mounts; nur Artefaktpfade + /dev/null etc.

## API / Schemas
- Interne Control-API (gRPC/HTTP, Vorschlag):
	- `UploadRequest { paths[], checkpoint_id, tier, compress, hash }`
	- `DownloadRequest { checkpoint_id, tier, dest_dir, decompress }`
	- `PrefetchRequest { checkpoint_id, target_tier, target_node }`
	- `StatusResponse { status, bytes, hash, error }`

## Sequenzdiagramm (Upload nach Checkpoint)
```
Agent -> Sidecar: UploadRequest(paths=CUDA+CRIU)
Sidecar -> Storage: put (tier1/2/3)
Sidecar -> Registry: metadata(hash, tier)
Agent <- Sidecar: status ok
```

## Sequenzdiagramm (Prefetch/Download)
```
Operator/Manager -> Sidecar: PrefetchRequest(checkpoint, target_tier)
Sidecar -> Storage: get (S3)
Sidecar -> Storage: write (NVMe/RAM)
Sidecar -> Agent: artifacts ready
```

## CRD-Beispiele
- Keine eigenen CRDs; arbeitet auf Basis von Requests des Operators (Checkpoint/Restore/Rebalance) und Registry-Einträgen.

## Performance Optimization: Zero-Copy Data Path

Um die Latenz gering zu halten und unnötige Speicherkopien zu vermeiden, wird ein Zero-Copy-Ansatz für den Datentransfer zwischen Node-Agent und Sidecar angestrebt.

**Konzept:**
Anstatt dass der Node-Agent die VRAM-Daten in den RAM liest, auf die NVMe schreibt, und das Sidecar sie dann wieder liest:

1.  **Shared Memory / Pipe:** Der Agent streamt die Daten direkt in eine Named Pipe oder einen Shared Memory Buffer.
2.  **Sidecar-Streaming:** Das Sidecar liest direkt aus dieser Pipe und streamt parallel nach S3 (Multipart Upload) und optional parallel auf NVMe (als Cache).
3.  **Vorteil:**
    *   Reduziert I/O-Last auf der lokalen Disk drastisch.
    *   Reduziert Latenz bis zum ersten Byte im S3.
    *   Spart RAM-Bandbreite.

**Implementierung:** Nutzung von `splice()` (Linux Syscall) um Daten vom Agent-Socket direkt in den Netzwerk-Socket des Sidecars zu schieben, ohne User-Space-Buffer-Kopien.
