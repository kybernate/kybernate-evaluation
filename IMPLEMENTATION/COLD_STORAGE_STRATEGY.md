# Cold Storage Strategy: Buildah Fallback

Während Kybernate primär auf "Hot/Warm" Restores via Shim-Intercept setzt (für Instant Resume), gibt es Szenarien, in denen ein Image-basierter Ansatz ("Cold Storage") sinnvoll ist.

## Strategie-Vergleich

| Feature | Hot/Warm (Shim Intercept) | Cold (Buildah Image) |
| :--- | :--- | :--- |
| **Ziel** | Instant Resume, Spot Recovery, Multiplexing | Langzeit-Archivierung, Cluster-Migration |
| **Format** | Raw Files (CRIU Images + VRAM Dumps) | OCI Container Image (Docker Image) |
| **Restore** | `runc restore` via Shim | `podman run` / `kubectl run` (Standard) |
| **Latenz** | < 1 Sekunde | Minuten (Push/Pull/Extract) |
| **GPU State** | **Erhalten** (VRAM Dump) | **Verloren** (meistens), da VRAM nicht im Image liegt |

## Buildah Integration (Optional)

Falls ein Checkpoint für die Langzeit-Archivierung ("Cold") markiert wird (z.B. via Policy `tier: cold-archive`), kann der Node Agent optional `buildah` nutzen.

### Workflow
1.  **Checkpoint:** Wie gewohnt (CUDA + CRIU).
2.  **Image Build:**
    *   Agent ruft `buildah from scratch` auf.
    *   Kopiert Checkpoint-Artefakte in das Image (`/checkpoint`).
    *   Setzt Annotationen für den Restore.
    *   Commit zum Image: `registry/my-app:checkpoint-v1`.
3.  **Push:** Upload in die Registry.

### Restore aus Cold Storage
*   Der User erstellt einen Pod, der dieses Image nutzt.
*   Der Kybernate Shim erkennt beim Start die Checkpoint-Daten *im* Image.
*   Er extrahiert sie temporär und führt dann den `runc restore` aus.
*   **Einschränkung:** GPU-VRAM-Dumps sind oft zu groß für Container-Images (Layer-Limits). Daher eignet sich dieser Pfad primär für CPU-only Workloads oder wenn der GPU-State separat (S3) geladen wird.

## Fazit
Buildah ist ein **Fallback** für Szenarien, in denen keine Kybernate-Runtime verfügbar ist oder Portabilität über Registry-Grenzen hinweg gefordert ist. Für den primären GPU-Multiplexing-Use-Case bleibt der Shim-Intercept der Standard.
