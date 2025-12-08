# End-to-End Test Workflow

Dieses Dokument beschreibt den manuellen Testablauf, um die Entwicklungsschritte von Kybernate zu validieren. Es definiert die Schritte vom Build über das Deployment bis hin zum Checkpoint/Restore-Zyklus.

## Voraussetzungen
*   MicroK8s installiert und laufend.
*   `microk8s` CLI konfiguriert.
*   NVIDIA Treiber und CUDA Toolkit installiert.

## Testablauf

### 1. Vorbereitung (Workload)
Zunächst muss der Test-Workload bereitstehen.
```bash
# Baut das Docker Image und pusht es in die lokale Registry (localhost:32000)
./IMPLEMENTATION/test-workload/build_and_push.sh
```

### 2. Clean State herstellen
Sicherstellen, dass keine alten Artefakte den Test stören.
```bash
# Lösche alte Pods
microk8s kubectl delete pod gpu-test-workload --force --grace-period=0 2>/dev/null
microk8s kubectl delete pod gpu-test-restored --force --grace-period=0 2>/dev/null

# Prüfe, ob GPU frei ist
nvidia-smi
# Erwartung: Keine Prozesse gelistet.
```

### 3. Build & Deploy Components
Die Kybernate-Komponenten werden frisch gebaut und in die MicroK8s-Umgebung integriert.

```bash
# 1. Binaries bauen
make build

# 2. MicroK8s stoppen (um Shim/Config sicher zu tauschen)
sudo microk8s stop

# 3. Binaries installieren
# Shim muss im Pfad von Containerd liegen (bei MicroK8s oft speziell)
sudo cp bin/containerd-shim-kybernate-v1 /var/snap/microk8s/current/bin/
sudo cp bin/kybernate-agent /usr/local/bin/

# 4. MicroK8s starten
sudo microk8s start

# 5. Node Agent starten (falls noch nicht als DaemonSet deployed)
# In separatem Terminal oder als Background Service
sudo kybernate-agent --socket /run/kybernate.sock &
```

### 4. Workload starten
```bash
microk8s kubectl apply -f IMPLEMENTATION/test-workload/pod.yaml

# Warten bis Running
microk8s kubectl wait --for=condition=Ready pod/gpu-test-workload

# Validierung
nvidia-smi
# Erwartung: python Prozess sichtbar, ~2GB VRAM belegt.
microk8s kubectl logs gpu-test-workload
# Erwartung: "Loop X: Wert=..., VRAM=..."
```

### 5. Checkpoint durchführen
Hier wird der Checkpoint-Prozess manuell getriggert (simuliert den Operator).

```bash
# Trigger via CLI Tool (oder curl auf Agent API)
# kybernate-ctl checkpoint --pod gpu-test-workload --dest /tmp/checkpoint-01

# Ablauf im Hintergrund:
# 1. Agent: cuCheckpointSave (VRAM -> RAM)
# 2. Agent: containerd.Task().Checkpoint() (CPU/Mem -> Images)
# 3. Agent: Metadata schreiben
```

### 6. Container löschen (Simulation Ausfall/Preemption)
```bash
microk8s kubectl delete pod gpu-test-workload --now

# Validierung
nvidia-smi
# Erwartung: GPU ist wieder komplett frei (0 Prozesse).
```

### 7. Restore durchführen
Der kritische Schritt: Wiederherstellung des Zustands.

```bash
# Erstellen eines neuen Pod-Manifests für den Restore
cat <<INNEREOF | microk8s kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-restored
  annotations:
    kybernate.io/restore-from: "/tmp/checkpoint-01"
spec:
  runtimeClassName: kybernate-gpu
  containers:
  - name: stress-gpu
    image: localhost:32000/kybernate-test-workload:latest
    resources:
      limits:
        nvidia.com/gpu: 1
INNEREOF
```

### 8. Validierung Restore
```bash
microk8s kubectl wait --for=condition=Ready pod/gpu-test-restored
microk8s kubectl logs gpu-test-restored
# Erwartung: Der Counter startet NICHT bei 0, sondern setzt fort (z.B. "Loop 15...").
# Erwartung: VRAM ist wieder bei ~2GB.
```

---

## Deep Dive: Warum "Shim Intercept" statt "Buildah Image"?

In der Kubernetes-Community (z.B. KubeCon 2025) wird oft ein Ansatz diskutiert, bei dem aus einem Checkpoint ein neues Docker-Image gebaut wird (`buildah`), welches dann normal gestartet wird.

**Kybernate verwendet diesen Ansatz bewusst NICHT.**

### Vergleich der Strategien

| Feature | Buildah / Image-Based Restore | Kybernate (Shim Intercept) |
| :--- | :--- | :--- |
| **Ablauf** | Checkpoint -> Tar -> `buildah build` -> Push Registry -> Pull -> Start | Checkpoint -> Raw Files (NVMe/S3) -> `runc restore` |
| **Latenz** | Hoch (Minuten). Image Layer Compression/Push/Pull kostet viel Zeit. | **Minimal (<1s)**. Daten bleiben lokal oder werden direkt gestreamt. |
| **GPU State** | Schwierig abzubilden. OCI Images sind für Filesysteme, nicht für VRAM-Dumps. | **Nativ**. VRAM-Dumps werden separat behandelt und via Driver API injiziert. |
| **Use Case** | Cold Migration, Archivierung, langsame Skalierung. | **Instant Resume**, Spot Instance Recovery, High-Freq Checkpointing. |

### Der Kybernate Restore Prozess im Detail

Da wir **Geschwindigkeit** priorisieren, umgehen wir den Umweg über die Image Registry:

1.  **Pod Creation:** Kubernetes erstellt einen "leeren" Pod basierend auf dem *ursprünglichen* Image. Das ist wichtig, damit K8s zufrieden ist (Image Pull Policy etc.).
2.  **Intercept:** Der `containerd-shim-kybernate-v1` erkennt: "Das ist kein neuer Start, das ist ein Restore".
3.  **Bypass:** Er ignoriert das leere Image für den Prozess-Start.
4.  **Injection:**
    *   Er weist `runc` an, den Prozesszustand (CPU/Mem) direkt aus den lokalen Checkpoint-Dateien wiederherzustellen (`runc restore`).
    *   Er weist den Node-Agent an, den GPU-Zustand (VRAM) direkt via CUDA API in den wiederhergestellten Prozess zu injizieren.
5.  **Resultat:** Der Prozess läuft sofort weiter. Für Kubernetes sieht es aus wie ein normaler Pod, aber der interne Zustand ist "alt".

Dies ermöglicht **Resume-Zeiten im Sub-Sekunden-Bereich** (aus RAM/NVMe), was mit dem Buildah-Ansatz physikalisch unmöglich wäre.
