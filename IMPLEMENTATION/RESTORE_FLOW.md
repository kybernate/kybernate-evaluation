# Restore Process & Portability

Dieses Dokument beschreibt den detaillierten Ablauf der Wiederherstellung (Restore) und erklärt, wie die Portabilität zwischen verschiedenen Nodes und GPUs sichergestellt wird.

## Detaillierter Restore-Ablauf

Der Restore-Prozess wird durch das Erstellen eines neuen Pods initiiert ("New Pod Strategy").

### 1. Scheduling & Allocation (Kubernetes Ebene)
1.  Der **Operator** erstellt einen neuen Pod (z.B. `vllm-worker-restored`) auf einem geeigneten Node (Node B).
2.  Der **Scheduler** weist den Pod Node B zu.
3.  Das **Device Plugin** auf Node B reserviert eine physische GPU (z.B. GPU Index 0) und setzt die Umgebungsvariablen (`NVIDIA_VISIBLE_DEVICES=0`, `KYB_GPU_SEAT=...`).

### 2. Container Creation (CRI / Shim Ebene)
4.  Das Kubelet ruft `CreateContainer` via CRI auf.
5.  Der **Kybernate Shim** fängt diesen Aufruf ab.
6.  Er erkennt die Annotation `kybernate.io/restore-from: <checkpoint-id>`.
7.  Anstatt `runc create` (Start from Scratch) aufzurufen, wechselt der Shim in den **Restore-Modus**.

### 3. Artifact Retrieval (Agent Ebene)
8.  Der Shim sendet einen `RestoreRequest` an den lokalen **Node Agent**.
9.  Der Agent prüft, ob die Checkpoint-Artefakte (CRIU Images, VRAM Dumps) lokal vorhanden sind.
10. Falls nicht (da Node-Wechsel), lädt das **Sidecar** die Daten von S3/Netzwerkspeicher herunter (oder streamt sie).

### 4. Process Restoration (CRIU Ebene)
11. Der Shim (oder Agent) ruft `runc restore` (bzw. `libcontainer.Restore`) auf.
12. **Wichtig:** Dabei wird der **Netzwerk-Namespace** des neuen Pods verwendet (nicht der alte aus dem Checkpoint). CRIU wird so konfiguriert, dass es die Netzwerk-Stacks nicht wiederherstellt, sondern die vom CNI-Plugin bereitgestellte Umgebung nutzt.
13. Der Prozessbaum wird im RAM wiederhergestellt, ist aber noch pausiert.

### 5. GPU Context Restoration (CUDA Driver API Ebene)
14. Der Agent identifiziert den wiederhergestellten Prozess.
15. Er nutzt die **CUDA Driver API (`cuCheckpointRestore`)**, um den CUDA-Kontext wiederherzustellen.
16. **Device Remapping:** Hier geschieht die Magie für die Portabilität.
    *   Der Agent liest die *neue* zugewiesene GPU-ID aus der Umgebung des neuen Pods (z.B. GPU 0).
    *   Er weist die CUDA-API an, den gespeicherten Kontext auf **dieser neuen Device-ID** wiederherzustellen.
    *   Die API mappt die virtuellen Adressen und Handles des Prozesses auf die physischen Ressourcen der neuen GPU.

### 6. Resume
17. Sobald VRAM und Kontext geladen sind, hebt der Shim die Pause auf.
18. Der Container läuft weiter, als wäre nichts geschehen.

---

## Portabilität (Cross-Node & Cross-GPU)

Das System ist explizit dafür ausgelegt, Workloads auf anderer Hardware fortzusetzen.

### Cross-Node Support
*   **Voraussetzung:** Ziel-Node muss gleiche CPU-Architektur (amd64) und kompatiblen Kernel haben.
*   **Dateisystem:** Da Kubernetes Container aus unveränderlichen Images startet, ist das Root-FS identisch. Änderungen im `RW-Layer` (z.B. temporäre Dateien) müssen im Checkpoint enthalten sein (`ctr checkpoint --rw`) oder die App muss stateless bzgl. lokaler Dateien sein.
*   **Netzwerk:** Die IP-Adresse ändert sich. Die Anwendung muss damit umgehen können (z.B. keine festen IPs in Configs binden, sondern auf `0.0.0.0` hören oder Service-Discovery nutzen).

### Cross-GPU Support
*   **Voraussetzung:** Die Ziel-GPU muss über ausreichend VRAM verfügen (mindestens so viel wie der Checkpoint benötigt) und eine kompatible Compute Capability (Architecture) haben.
*   **PCIe-Slot Unabhängigkeit:**
    *   Der Checkpoint speichert **keine** physischen PCIe-Adressen (z.B. `0000:01:00.0`).
    *   Er speichert den logischen CUDA-Kontext.
    *   Beim Restore mappt der Agent diesen Kontext auf das Device, das ihm vom Device Plugin zugewiesen wurde.
    *   **Beispiel:** Training lief auf Node A (GPU 1, PCIe Bus 0x41). Restore auf Node B (GPU 0, PCIe Bus 0x03). -> **Funktioniert**, da die CUDA Driver API die Abstraktion übernimmt.

### Einschränkungen
*   **Hardware-Generation:** Ein Restore von einer Ampere-GPU (A100) auf eine ältere Pascal-GPU (P100) funktioniert oft nicht, wenn der Code Features nutzt, die die alte Karte nicht hat. Umgekehrt (alt auf neu) ist meist kompatibel.
*   **Driver Version:** Der NVIDIA Treiber auf dem Ziel-Node muss gleich oder neuer sein als auf dem Quell-Node.
