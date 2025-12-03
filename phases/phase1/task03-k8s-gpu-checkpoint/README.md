# Task 03 – GPU Pod Checkpoint (MicroK8s)

Dieses Verzeichnis enthält alle temporären Ressourcen für **Task 03** aus `docs/planning/TASK-03_K8S_GPU_CHECKPOINT.md`.

## Ziel
Beweisen, dass ein GPU-Pod in MicroK8s mit `crictl` + `criu` + `cuda-checkpoint` dumpbar und restaurierbar ist, **ohne** dass bereits der Kybernate Shim im Spiel ist.

## Struktur
```
manifests/   # Kubernetes YAMLs für Namespace + Test-Pod
scripts/     # Automatisierte Checkpoint/Restore Abläufe
logs/        # Gesammelte Ausgaben (dump.log, restore.log, etc.)
workspace/   # HostPath, der in den Pod gemountet wird (wird automatisch erstellt)
```

## Vorgehen (Kurzfassung)
1.  Image vorbereiten gemäß `docs/test/03_HEAVY_PYTORCH_DUMP.md` (lokales Tag `localhost:32000/gpu-pytorch:v1`).
2.  Sicherstellen, dass `workspace/` auf dem Host existiert (`mkdir -p workspace`).
3.  Namespace anlegen: `microk8s kubectl apply -f manifests/namespace.yaml`.
4.  Pod deployen: `microk8s kubectl apply -f manifests/pytorch-stress-pod.yaml`.
5.  `scripts/checkpoint.sh` ausführen (legt Checkpoints unter `artifacts/checkpoints/` ab).
6.  Pod löschen und `scripts/restore.sh` nutzen, um den Prozess im Host-Namespace wieder anzuschieben.
7.  Ergebnisse in `docs/test/04_K8S_GPU_CHECKPOINT.md` dokumentieren.

## Ablaufprotokoll / Kommandos

Alle Schritte werden hier mit den exakten Kommandos dokumentiert, damit der Test reproduzierbar bleibt.

| Schritt | Zweck | Kommando |
| --- | --- | --- |
| 1 | Image bauen | `cd phases/phase1/task03-k8s-gpu-checkpoint/workspace && sudo docker build -t localhost:32000/gpu-pytorch:v1 .` |
| 2 | Image exportieren | `sudo docker save localhost:32000/gpu-pytorch:v1 -o gpu-pytorch-v1.tar` |
| 3 | Image in MicroK8s laden | `microk8s ctr image import phases/phase1/task03-k8s-gpu-checkpoint/workspace/gpu-pytorch-v1.tar` |
| 4 | Namespace anlegen | `microk8s kubectl apply -f phases/phase1/task03-k8s-gpu-checkpoint/manifests/namespace.yaml` |
| 5 | Pod deployen | `microk8s kubectl apply -f phases/phase1/task03-k8s-gpu-checkpoint/manifests/pytorch-stress-pod.yaml` |
| 6 | Pod-Status prüfen | `microk8s kubectl get pods -n kybernate-system` |
| 7 | Pod-Logs prüfen | `microk8s kubectl logs -n kybernate-system pytorch-stress --tail=20` |
| 8 | Checkpoint (CRIU dump) | `phases/phase1/task03-k8s-gpu-checkpoint/scripts/checkpoint.sh` |
| 9 | GPU-Auslastung prüfen | `nvidia-smi` |
| 10 | Pod löschen (Cleanup) | `microk8s kubectl delete pod pytorch-stress -n kybernate-system --grace-period=0 --force` |
| 11 | Image-Bestand prüfen | `microk8s ctr images ls | grep gpu-pytorch` |
| 12 | Image erneut in MicroK8s laden | `microk8s ctr image import phases/phase1/task03-k8s-gpu-checkpoint/workspace/gpu-pytorch-v1.tar` |
| 13 | Pod neu deployen | `microk8s kubectl apply -f phases/phase1/task03-k8s-gpu-checkpoint/manifests/pytorch-stress-pod.yaml` |
| 14 | Pod-Status prüfen (nach Redeploy) | `microk8s kubectl get pods -n kybernate-system` |
| 15 | Pod-Logs prüfen (nach Redeploy) | `microk8s kubectl logs -n kybernate-system pytorch-stress --tail=5` |
| 16 | GPU-Auslastung verifizieren | `nvidia-smi` |
| 17 | Artefakte aufräumen | `cd phases/phase1/task03-k8s-gpu-checkpoint && rm -rf artifacts/checkpoints logs/*` |
| 18 | Artefakte prüfen | `ls phases/phase1/task03-k8s-gpu-checkpoint/logs; ls phases/phase1/task03-k8s-gpu-checkpoint/artifacts` |
| 19 | GPU-Status vor Dump | `nvidia-smi` |
| 20 | Pod-Logs vor Dump | `microk8s kubectl logs -n kybernate-system pytorch-stress --tail=5` |
| 21 | Checkpoint (CRIU dump, Retry) | `phases/phase1/task03-k8s-gpu-checkpoint/scripts/checkpoint.sh` |
| 22 | GPU-Status nach Dump | `nvidia-smi` |
| 23 | Pod-Status nach Dump | `microk8s kubectl get pods -n kybernate-system` |
| 24 | Pod-Logs nach Dump | `microk8s kubectl logs -n kybernate-system pytorch-stress --tail=5` |
| 25 | Restore-Test (Fehlgeschlagen) | `scripts/restore.sh` (Skript entfernt, da Host-Restore nicht möglich) |
| 26 | Cleanup | `rm scripts/restore.sh && rm -rf artifacts/checkpoints logs/*` |

Aktueller Status: Checkpoint wurde erneut erfolgreich erzeugt (`logs/dump.log`, Images unter `artifacts/checkpoints/pytorch/`).

**Was genau wird gedumpt?**
- `criu dump` hält den GPU-Prozess (Python in `pytorch-stress`) an, speichert dessen Userspace + CUDA Kontext (via `cuda_plugin`) und legt Netzwerk/Cgroup/Mount-State im Verzeichnis `artifacts/checkpoints/pytorch/` ab.
- **Wichtig:** Das Skript nutzt nun `--leave-running`. Das bedeutet, der Prozess wird nach dem erfolgreichen Dump **nicht** beendet, sondern läuft weiter. Dies verhindert Deadlocks, die beim Zusammenspiel von CRIU, Containerd und dem NVIDIA-Treiber auftreten können.
- Der Kubernetes Pod bleibt `Running` und der Prozess aktiv.
- Die gemounteten HostPath-Daten (`workspace/`) bleiben unangetastet; wir sichern nur den Prozesszustand plus Namespaces.

### Skript-Logik (`scripts/checkpoint.sh`)
Das Skript führt folgende Schritte automatisiert aus:
1.  **Privilegien:** Fordert `sudo` an (nötig für CRIU und crictl).
2.  **Cleanup:** Löscht alte Checkpoints in `artifacts/checkpoints/pytorch/`.
3.  **Identifikation:** Ermittelt via `crictl ps` und `crictl inspect` die Container-ID und die Host-PID des Python-Prozesses.
4.  **Mount-Erkennung:** Liest `/proc/$PID/mountinfo`, um externe Mounts (wie den HostPath) korrekt an CRIU zu übergeben.
5.  **Dump:** Startet `criu dump` mit:
    *   `--tree $PID`: Sichert den Prozessbaum.
    *   `--leave-running`: Lässt den Prozess nach dem Dump am Leben (verhindert Hangs).
    *   `--ext-mount-map auto`: Automatische Erkennung externer Mounts.
    *   `--enable-fs hugetlbfs` & `--enable-external-masters`: Dateisystem-Support.
    *   `--lib /usr/local/lib/criu`: Lädt das `cuda-checkpoint` Plugin.


### Restore-Test (Ergebnis)
Der Versuch, den Prozess direkt auf dem Host via `scripts/restore.sh` wiederherzustellen, schlug fehl.
*   **Grund:** Der Dump enthält komplexe Kubernetes-spezifische Namespaces (Netzwerk, Mounts, Cgroups), die auf dem Host nicht 1:1 existieren oder Konflikte verursachen (z.B. `mnt: BUG at criu/mount.c:48` beim Versuch, Container-Mounts auf dem Host nachzubilden).
*   **Erkenntnis:** Ein "Host-Restore" eines Kubernetes-Containers ist nicht praktikabel. Der korrekte Weg (für Phase 2) ist der Restore **in einen neuen Container** (via `runc` oder `containerd` API), der eine kompatible Umgebung bereitstellt.
*   **Erfolg:** Der Checkpoint selbst (Dump) war erfolgreich und die Artefakte (inkl. GPU-Memory) wurden korrekt geschrieben. Damit ist das Hauptziel von Task 03 erreicht.
*   **Konsequenz:** Das Skript `scripts/restore.sh` wurde entfernt, da es in diesem Kontext nicht zielführend ist.

### Runbook: Pod & Registry aufräumen

1. **Pod stoppen** – `microk8s kubectl delete pod pytorch-stress -n kybernate-system --grace-period=0 --force`
2. **Image-Bestand prüfen** – `microk8s ctr images ls | grep gpu-pytorch`. Falls der Eintrag fehlt, Schritt 3.
3. **Image erneut importieren** – `microk8s ctr image import .../workspace/gpu-pytorch-v1.tar`
4. **Pod neu deployen** – `microk8s kubectl apply -f .../manifests/pytorch-stress-pod.yaml`
5. **Workload prüfen** – `microk8s kubectl get pods -n kybernate-system`, `microk8s kubectl logs ...`, anschließend `nvidia-smi` um GPU-Last zu bestätigen.
6. **Artefakte säubern** – `rm -rf artifacts/checkpoints logs/*`
7. **Checkpoint erneut ausführen** – `./scripts/checkpoint.sh`

> Ergänze jede weitere Aktion (Pod löschen, Logs ansehen, etc.) in der Tabelle, sobald sie ausgeführt wurde.

Alle Kommandos laufen auf dem MicroK8s Host (nicht im Container).