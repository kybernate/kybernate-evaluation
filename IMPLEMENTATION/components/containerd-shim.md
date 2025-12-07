# Containerd Shim (GPU-aware)

## Zweck
- Interzeptiert Pod-/Container-Lifecycle (pause/stop/start) und führt GPU-spezifische Hooks aus.
- Stellt sicher, dass vor CRIU immer ein erfolgreicher CUDA-Checkpoint (API) erfolgt, der VRAM → RAM schiebt und den CUDA-Kontext lockt.
- Hebt nach Restore die Pause auf und synchronisiert mit dem Node-Agent.

## Verantwortlichkeiten
- Pre-Hook: I/O quiesce, Streams sync, neue GPU-Work blocken; RPC zum Node-Agent: „CUDA checkpoint now“.
- Post-Hook: Resume nach CRIU-Restore; Fehler-Propagation an Operator.
- Kontextübergabe: Container-/Task-IDs, GPU-Seat/Group-Infos aus Device Plugin (Env/Annotations) an Node-Agent durchreichen.

## Schnittstellen
- Eingehend: Containerd Shim API (Task lifecycle), ggf. CRI-Events.
- Ausgehend: gRPC/Unix Domain Socket zum Node-Agent (CUDA checkpoint, CRIU trigger, status).
- Metadata: schreibt Status/Events zurück (Logs, optional CRD-Event via Agent/Operator).

## Abläufe (Kurz)
1) Pre-Hook → Agent: CUDA checkpoint (API), lock, VRAM→RAM.
2) Agent meldet done → Shim startet CRIU-Dump.
3) Nach Dump → Sidecar übernimmt Packaging.
4) Restore: Agent ruft CRIU-Restore, Shim wartet und hebt Pause auf.

## Fehlerpfade
- CUDA-Checkpoint fehlgeschlagen: Abbruch, Status zurück an Operator, keine CRIU-Ausführung.
- CRIU-Dump fehlgeschlagen: Fehlerstatus, optional Retry/Backoff.
- Timeout-Schutz pro Phase.

## Platzierung im Monorepo
- Code: `shim/containerd-shim-kybernate-v1/` (analog bestehender Shim-Code), ggf. neuer Unterordner `pkg/shim/hooks`.
- Binaries: `shim/bin/containerd-shim-kybernate-v1`.
- Tests: `shim/containerd-shim-kybernate-v1/internal/...` oder `pkg/shim/hooks/..._test.go`.

## Konfiguration
- Socket-Pfad zum Node-Agent.
- Timeouts (CUDA checkpoint, CRIU dump/restore).
- Logging/Tracing.

## Security
- Least privilege: minimaler Satz an Capabilities; keine breiten Host-Mounts außer benötigten Sockets/Dev-Nodes.
- Validierung der Seat/Group-Daten aus Device Plugin gegen Runtime-Info.

## API Schema (Shim ↔ Agent)
- gRPC (UDS) Suggested:
	- `CheckpointRequest { container_id, pod_uid, gpu_group, gpu_seat, timeout_ms }`
	- `CheckpointReply { status, cuda_done, criu_done, error }`
	- `RestoreRequest { container_id, pod_uid, gpu_group, gpu_seat, checkpoint_ref }`
	- `RestoreReply { status, error }`

## Sequenzdiagramm (Checkpoint)
```
Shim -> Agent: CheckpointRequest(container)
Agent -> CUDA API: lock + VRAM->RAM
Agent -> Agent: emit cuda_done
Shim <- Agent: cuda_done
Shim -> Agent: start CRIU dump
Agent -> CRIU: dump
Shim <- Agent: status ok
Shim -> Sidecar (indirekt via Agent fs): artifacts ready
```

## Sequenzdiagramm (Restore)
```
Shim -> Agent: RestoreRequest(container, checkpoint_ref)
Agent -> Sidecar/FS: fetch artifacts (prefetched)
Agent -> CRIU: restore
Shim <- Agent: restore ok
Shim -> runtime: unpause
```

## CRD-Beispiele
- Keine eigenen CRDs; konsumiert Operator-CRDs indirekt. Relevant: `CheckpointRequest`, `RestoreRequest` CRDs werden vom Operator in RPCs übersetzt.
