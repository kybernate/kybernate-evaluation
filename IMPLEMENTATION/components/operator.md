# Operator / Controller

## Zweck
- Deklariert und reconciled Policies/Requests für Checkpoint, Restore, Tiering, Rebalancing, Multiplexing, Scale-to-Zero.
- Steuert Prefetch/Promotion und Zielplatzierung (Node/GPU-Group/Seat).

## CRDs (Beispiele)
- `CheckpointPolicy`: Regeln für Trigger (Idle, Timer, Preemption), Tiers (0–3), SLOs.
- `CheckpointRequest`: ad-hoc oder durch Policy erzeugt; referenziert Pod/Workload.
- `RestoreRequest`: gewünschter Checkpoint + Ziel-Node/GPU-Group; optional Warmup-Level.
- `TierPlacement`: gewünschte Tiers für Artefakte (hot/warm/cold).
- `RebalanceRequest`: Quell-/Ziel-GPU-Group/Node, Priorität.

## Verantwortlichkeiten
- Active Seat Auswahl (Multiplexing): welches Modell ist in VRAM aktiv.
- Scale-to-Zero: Idle Pods checkpointen und Tiers herabstufen.
- Rebalancing: Pause+Checkpoint auf Quelle, Prefetch+Restore auf Ziel; Koordination mit Device Plugin.
- Prefetch/Promotion: Cold→Warm→Hot je nach Traffic/SLO.
- Status/Events: CR-Status, K8s Events, Metriken.

## Schnittstellen
- Kubernetes API (CRDs), Watches auf Pods/Nodes/Device-Plugin-Allocations.
- gRPC/UDS indirekt über Node-Agent/Sidecar via Requests.
- Registry/Metadata Service für Auswahl/Verifikation.

## Platzierung im Monorepo
- Code: `cmd/kyb-operator/` (main) + `pkg/operator/...` (Reconcilers, services).
- CRD-Spez: `config/crd/bases/*.yaml` oder `manifests/crd/*.yaml`.
- RBAC/Manifeste: `config/rbac/`, `manifests/operator/`.
- Tests: `pkg/operator/..._test.go`.

## Implementierungshinweise
- Controller-Runtime (Go) + Informers für Pods/Nodes/CRDs.
- Rate-Limits/Backoff pro Request-Typ.
- SLO-Annotationen in Status (z.B. Resume-Tier, Expected-Resume-Sec).
- Reconcile-Graph: Policies → Requests → Agent Calls → Registry Update.

## Security
- RBAC least-privilege; nur eigene CRDs + benötigte Pod/Node-Lesesichten.
- Kein direkter Zugriff auf GPU-Devices; Steuerpfad-only.

## API / Schemas
- CRDs (YAML-Skizzen):

`CheckpointPolicy`
```yaml
apiVersion: kyb.io/v1alpha1
kind: CheckpointPolicy
spec:
	triggers:
		idleSeconds: 300
		intervalSeconds: 900
		preemption: true
	tiers:
		hot: ram
		warm: nvme
		cold: s3
	slo:
		resumeRamSeconds: 1
		resumeNvmeSeconds: 5
```

`CheckpointRequest`
```yaml
apiVersion: kyb.io/v1alpha1
kind: CheckpointRequest
spec:
	targetRef:
		namespace: default
		name: my-vllm
	policyRef: default-policy
	priority: high
	deadlineSeconds: 60
```

`RestoreRequest`
```yaml
apiVersion: kyb.io/v1alpha1
kind: RestoreRequest
spec:
	checkpointRef: cp-my-vllm-2024-01-01T10:00:00Z
	targetNode: gpu-node-b
	targetGroup: G01
	warmupTier: ram
```

`RebalanceRequest`
```yaml
apiVersion: kyb.io/v1alpha1
kind: RebalanceRequest
spec:
	sourceGroup: G01
	targetGroup: G23
	reason: load-spread
```

## Sequenzdiagramm (Policy → Checkpoint)
```
Policy Watch -> Operator: trigger
Operator -> Shim/Agent: CheckpointRequest RPC
Agent -> CUDA API/CRIU: dump
Agent -> Sidecar: package
Sidecar -> Registry: metadata update
Operator <- Registry: ack
```

## Sequenzdiagramm (Restore / Rebalance)
```
User/Traffic -> Operator: RestoreRequest/RebalanceRequest
Operator -> TierManager/Sidecar: Prefetch Tier3->2->1
Operator -> Shim/Agent (target node): RestoreRequest
Agent -> CRIU/CUDA: restore + warmup (optional)
Shim -> runtime: unpause
Operator: set Active Seat, update status
```
