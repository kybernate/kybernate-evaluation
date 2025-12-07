# Metadata / Registry Service

## Zweck
- Hält Metadaten zu Checkpoints (IDs, Hashes, Timestamps, Size, Tier-Ort, Ziel-Node/GPU-Group, Seat).
- Liefert Lookup/Query für Operator/Agent/Sidecar.
- Basis für Konsistenz, Auswahl und Rebalancing-Entscheidungen.

## Verantwortlichkeiten
- Persistenz von Checkpoint-Records; Versions-/Generationsverwaltung.
- Atomare Updates bei neuen Checkpoints (write-once + validate hash).
- API für Lookup nach Kriterien (jüngster, bestimmtes Tier, bestimmtes Modell).
- GC/Retention nach Policy.

## Schnittstellen
- gRPC/HTTP API für Operator/Agent/Sidecar.
- Optionale Watch/Events für neue/aktualisierte Artefakte.
- Backend: z.B. Postgres/SQLite oder leichtgewichtiger KV (Badger/etcd-namespaced).

## Platzierung im Monorepo
- Code: `pkg/registry/` (API + Storage-Backend), `pkg/registry/server` (service), `pkg/registry/client` (SDK).
- Deployment: eigenständiger Service (Deployment/StatefulSet) oder eingebettet im Operator (leichtgewichtiger Modus).
- Manifeste: `manifests/registry/`.

## Implementierungshinweise
- Schema: Checkpoint(id, model, version, hash, size, tier, node, group, seat, created_at, expires_at).
- Indizes für Queries: (model, version, tier), (node, group), created_at.
- Konsistenz: nur validierte Hashes werden committed; CAS/transactions.

## Security
- AuthN/Z per ServiceAccount/JWT; TLS für API.
- Rollen: reader (Operator/Sidecar), writer (Agent/Sidecar on checkpoint), admin (GC/maintenance).

## API / Schemas (gRPC/HTTP)
- `PutCheckpoint { id, model, version, hash, size, tier, node, group, seat, created_at }`
- `GetCheckpoint { id } -> { record }`
- `ListCheckpoints { model?, version?, tier?, node?, group?, limit }`
- `UpdateTier { id, tier, node }`
- `DeleteCheckpoint { id }`

## Sequenzdiagramm (Put + Verify)
```
Sidecar/Agent -> Registry: PutCheckpoint(meta, hash)
Registry -> Storage (optional HEAD): verify size/hash
Registry: commit record
Sidecar/Agent <- Registry: ack
```

## Sequenzdiagramm (Lookup for Restore)
```
Operator -> Registry: ListCheckpoints(model=X, tier>=nvme)
Registry -> Operator: records
Operator -> TierMgr/Sidecar: Prefetch(checkpoint_id)
```

## CRD-Beispiele
- Keine separaten CRDs; Registry ist Dienst/DB. Operator-CRDs referenzieren Checkpoints via `checkpointRef`.
