# Storage Tier Manager

## Zweck
- Orchestriert HSM-ähnliches Tiering: Promotion/De-Promotion von Artefakten zwischen Tier 3 (S3/Share) → Tier 2 (NVMe) → Tier 1 (RAM) → Tier 0 (VRAM via Agent).
- Platziert Artefakte nahe am Ziel-Node/GPU für Rebalancing/Instant Resume.

## Verantwortlichkeiten
- Hotness/LRU/SLO-Policy auswerten, Promotion-Jobs planen.
- Cross-Node-Platzierung: Warm-Kopien auf Ziel-Node bevor Restore/Rebalance.
- Budget/Quota beachten (NVMe/RAM Limits).
- Trigger für Prefetch/Promotion an Sidecar/Agent/Operator liefern.

## Schnittstellen
- Registry: liest/schreibt Artefakt-Standorte, Hashes, Timestamps.
- Sidecar/Agent: ruft Prefetch/Promotion APIs; empfängt Completion/Fehler.
- Operator: nimmt Policies entgegen, meldet Ergebnisse/Telemetry.

## Abläufe (Beispiel)
1) Policy sieht Traffic-Spike → promote Checkpoint X von S3 nach NVMe (Ziel-Node).
2) Fertig → optional weiter nach RAM (Tier1) kurz vor Resume.
3) Nach Idle → demote zurück auf NVMe/S3.

## Platzierung im Monorepo
- Code: `pkg/tiering/manager/` (Go), Jobs/Workers unter `pkg/tiering/jobs/...`.
- CRD-Logik im Operator oder eigener Controller: `RebalanceRequest`/`TierPlacement`.
- Manifeste: falls eigener Deployment/Controller: `manifests/tiering-manager/`.

## Implementierungshinweise
- Worker-Queue mit Rate-Limits; Prioritäten (SLO-basiert).
- Idempotente Transfers; Hash-Validierung nach Kopie.
- Hintergrund-GC für alte Artefakte.

## Security
- Minimal notwendige Credentials für Remote-Stores; isolierte ServiceAccount.
- Schreibrechte nur auf definierte Pfade/Buckets.

## API / Schemas
- Interner gRPC/HTTP Vorschlag:
	- `PromotionRequest { checkpoint_id, source_tier, target_tier, target_node }`
	- `DemotionRequest { checkpoint_id, source_tier, target_tier }`
	- `PlacementPlan { checkpoint_id, targets: [ {tier,node} ], deadline }`
	- `PromotionStatus { status, bytes_moved, hash_verified, error }`

## Sequenzdiagramm (Promotion)
```
Operator -> TierMgr: PromotionRequest(cp, S3->NVMe, nodeB)
TierMgr -> Storage(S3): get
TierMgr -> Storage(NVMe@nodeB): put
TierMgr -> Sidecar/Agent: notify ready
Operator <- TierMgr: status
```

## Sequenzdiagramm (Demotion/GC)
```
TierMgr: detect idle -> DemotionRequest(cp, NVMe->S3)
TierMgr -> Storage(NVMe): read
TierMgr -> Storage(S3): write
TierMgr: verify hash, delete NVMe copy
```

## CRD-Beispiele
- Kann über Operator-CRDs abgebildet werden, z.B. `TierPlacement`:
```yaml
apiVersion: kyb.io/v1alpha1
kind: TierPlacement
spec:
	checkpointRef: cp-my-vllm
	desired:
		- tier: nvme
			node: gpu-node-b
		- tier: ram
			node: gpu-node-b
	ttlSecondsAfterIdle: 1800
```
