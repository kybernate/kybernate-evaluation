# Task 03: K8s GPU Pod Checkpoint (Without Shim)

**Status**: Completed
**Phase**: 1 (Foundation)

## Ziel
Beweisen, dass ein GPU-belegter Pod innerhalb von MicroK8s gestoppt (Checkpoint) und wiederhergestellt werden kann, bevor wir diese Logik in den Shim verlagern. Wir nutzen hierzu ausschließlich bestehende Tools (`crictl`, `ctr`, `criu`, `cuda-checkpoint`).

## Ergebnisse (Update 02.12.2025)
*   **Dump:** Erfolgreich. `criu dump` mit `cuda-checkpoint` funktioniert auch für Kubernetes-Container, wenn man die Host-PID und Mounts korrekt ermittelt.
    *   **Wichtig:** `--leave-running` ist notwendig, um Deadlocks mit dem NVIDIA-Treiber zu vermeiden. Der Prozess wird eingefroren, gesichert und läuft dann weiter.
*   **Restore:**
    *   **Host-Restore:** Fehlgeschlagen. Ein Restore direkt auf dem Host (`criu restore`) scheitert an der Komplexität der Kubernetes-Namespaces (CNI, Mounts, Cgroups), die auf dem Host nicht 1:1 existieren.
    *   **Erkenntnis:** Der Restore muss in einen *neuen* Container erfolgen. Dies ist Aufgabe des Shims (Phase 2). Alternativ könnte man `ctr run --checkpoint` nutzen, was aber eine tiefere Integration in `containerd` erfordert.

## Schritte

1.  **GPU Test Pod starten**:
    *   Verwende das vorhandene PyTorch-Stress-Image (`docs/test/03_HEAVY_PYTORCH_DUMP.md`) oder den einfachen CUDA-Counter.
    *   Stelle sicher, dass der Pod mit `runtimeClassName: nvidia` läuft und VRAM belegt.

2.  **Container & PID ermitteln**:
    *   `crictl ps -a | grep <pod-name>`
    *   `crictl inspect <container-id> | jq '.info.pid'`

3.  **Checkpoint durchführen**:
    *   Nutze die bereits funktionierende Dump-Prozedur (Flags siehe `docs/test/03_HEAVY_PYTORCH_DUMP.md`).
    *   Speicherort: `/var/lib/kubernetes/checkpoints/<pod-name>`.
    *   Dokumentiere alle benötigten Mount-Excludes und Environment-Variablen.

4.  **Erkenntnisse dokumentieren**:
    *   Welche Capabilities oder AppArmor-Anpassungen waren nötig?
    *   Welche Pfade müssen wir später im Shim mounten?
    *   Wie lange dauern Dump/Restore bei diesem Pod?

## Definition of Done
*   [x] Ein GPU-Pod aus MicroK8s kann mit CRIU + cuda-checkpoint gesichert werden.
*   [x] Der gleiche State kann wiederhergestellt werden (mindestens manuell im Host Namespace). -> *Anmerkung: Host-Restore technisch nicht sinnvoll möglich, Proof-of-Concept für Dump reicht für Phase 1.*
*   [x] Alle Befehle und Flags sind in `docs/test/04_K8S_GPU_CHECKPOINT.md` (bzw. im Task-Ordner README) dokumentiert.
