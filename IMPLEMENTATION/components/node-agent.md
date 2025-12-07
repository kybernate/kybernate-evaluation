# Node-Agent (DaemonSet)

## Zweck
- Führt CUDA Checkpoint API aus (lock, VRAM→RAM) und startet/steuert CRIU (dump/restore) pro Pod/Container.
- Stellt einen lokalen gRPC/UDS-Service bereit für Shim/Operator.
- Unterstützt Rebalancing (Checkpoint auf Node A, Restore auf Node B).

## Verantwortlichkeiten
- CUDA-Checkpoint: Kontext locken, VRAM in RAM kopieren, GPU-Dump erzeugen, Status liefern.
- CRIU: dump/restore orchestrieren, Namespace/FD/CPU/Memory sichern/wiederherstellen.
- Artefakt-Staging: Übergabe an Sidecar (für Packaging) bzw. Empfang beim Restore.
- Prefetch-Trigger: auf Anweisung Tier3→2→1 Download starten (über Sidecar/StorageMgr).
- Health/Metrics: Dauer pro Phase, Fehlercodes, GPU/Seat-Bezug.

## Schnittstellen
- gRPC (UDS) für Shim/Operator: `CheckpointRequest`, `RestoreRequest`, Status/Events.
- Lokale IPC/Filesystem: Pfade für CUDA/CRIU Dumps, Übergabe an Sidecar.
- Device Plugin Daten: GPU-Indices, Seat/Group aus Env/Annotations.

## Abläufe (Happy Path Checkpoint)
1) Shim ruft `Checkpoint` → Agent.
2) Agent: CUDA API aufrufen (lock, VRAM→RAM, GPU-Dump).
3) Erfolg → Agent startet CRIU dump (CPU/Memory/FD/NS).
4) Ergebnis/Status an Shim + Sidecar (für Packaging/Tiering).

## Abläufe (Restore)
1) Operator/Traffic-Trigger → Prefetch (Sidecar/TierMgr) auf Ziel-Node.
2) Agent: CUDA-Dumps laden (RAM), optional VRAM-Warmup; CRIU restore ausführen.
3) Status an Shim, Pause aufheben.

## Fehlerpfade
- CUDA-Checkpoint fail → abbrechen, kein CRIU; Status an Shim/Operator.
- CRIU fail → Status, optional Retry/Backoff; Artefakt-Rotation.
- Mismatch GPU/Seat → Fehler, kein Restore.

## Platzierung im Monorepo
- Code: `pkg/runtime/agent/` oder `shim/pkg/service/agent/` (Go). gRPC defs unter `pkg/api/agent/` (proto).
- DaemonSet Manifeste: `manifests/agent/daemonset.yaml`.
- Binaries: `bin/kyb-node-agent`.

## Konfiguration
- Pfade: UDS für gRPC, Dump-Verzeichnisse (tmpfs/NVMe), CRIU binary path.
- Timeouts für CUDA/CRIU.
- Tiering-Policy Hooks (wohin Artefakte standardmäßig geschrieben werden).

## Security
- Läuft als root (CRIU-Anforderungen), minimal nötige Capabilities.
- Zugriff auf `/dev/nvidia*`, cgroups, namespaces.
- AuthZ für Requests (nur Shim/Operator via UDS, File-permissions).

## API Schema (gRPC Vorschlag)
- `CheckpointRequest { container_id, pod_uid, namespace, gpu_group, gpu_seat, checkpoint_policy_ref, timeout_ms }`
- `CheckpointReply { status, cuda_done, criu_done, artifact_paths[], error }`
- `RestoreRequest { container_id, pod_uid, namespace, gpu_group, gpu_seat, checkpoint_ref, warmup: bool }`
- `RestoreReply { status, error }`
- `PrefetchRequest { checkpoint_ref, target_tier, target_node }`

## Sequenzdiagramm (Checkpoint Happy Path)
```
Shim -> Agent: CheckpointRequest
Agent -> CUDA API: lock + VRAM->RAM + dump
Agent -> CRIU: dump
Agent -> Sidecar: handoff artifacts dir
Shim <- Agent: status ok (cuda_done=true, criu_done=true)
```

## Sequenzdiagramm (Restore Happy Path)
```
Operator -> Sidecar/Manager: Prefetch(checkpoint, target_node)
Shim -> Agent: RestoreRequest
Agent -> Sidecar: artifacts ready?
Agent -> CRIU: restore
Agent -> CUDA API: (optional) VRAM warmup
Shim <- Agent: status ok
Shim -> runtime: unpause
```

## CRD-Beispiele (vom Operator genutzt)
- `CheckpointRequest` CRD (Spec: target Pod, policyRef, priority, deadlineMs)
- `RestoreRequest` CRD (Spec: checkpointRef, targetNode/Group/Seat, warmupLevel)
- `RebalanceRequest` CRD (Spec: sourceNode/Group, targetNode/Group, reason)

## Implementation Details: CUDA Driver API

**WICHTIG:** Der Node-Agent darf **nicht** das `cuda-checkpoint` Binary wrappen oder aufrufen.
Stattdessen muss die **NVIDIA CUDA Driver API** direkt via CGO angesprochen werden.

Referenz: [CUDA Driver API - Checkpoint](https://docs.nvidia.com/cuda/cuda-driver-api/group__CUDA__CHECKPOINT.html)

Die Implementierung in `pkg/checkpoint/cuda` muss folgende C-Funktionen binden:
*   `cuCheckpointSave` (zum Erstellen des VRAM-Dumps)
*   `cuCheckpointRestore` (zum Wiederherstellen)
*   `cuCtxCreate` / `cuCtxDestroy` (Kontext-Management)

Dies ermöglicht eine feingranulare Kontrolle über den Checkpoint-Prozess, besseres Error-Handling und vermeidet die Abhängigkeit von externen Binaries im Container/Host.

