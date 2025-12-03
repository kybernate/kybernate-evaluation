# Test 04: GPU Pod Checkpoint in MicroK8s

Dieses Dokument beschreibt, wie ein laufender GPU-Pod innerhalb von MicroK8s mit CRIU und `cuda-checkpoint` pausiert und wiederhergestellt werden kann – ohne den Kybernate Shim.

## Voraussetzungen
*   Cluster aus `docs/setup/02_INSTALL_MICROK8S.md` (inkl. NVIDIA Addon).
*   Tools aus `docs/setup/03_INSTALL_DEVELOPMENT_DEPENDENCIES.md`.
*   Test-Image aus `docs/test/03_HEAVY_PYTORCH_DUMP.md` oder Counter.

## Schritte

### 1. Pod starten
```bash
microk8s kubectl apply -f gpu-pytorch-pod.yaml  # Siehe Test 03
microk8s kubectl logs -f pytorch-stress
```

### 2. Container & PID herausfinden
```bash
CONTAINER_ID=$(crictl ps | grep pytorch-stress | awk '{print $1}')
crictl inspect $CONTAINER_ID | jq '.info.pid'
```

### 3. Mount-Excludes erzeugen
```bash
sudo cat /proc/$PID/mountinfo | grep -E "nvidia|etc/hosts|resolv.conf" | \
  awk '{print "--external mnt[" $1 "]:" $5}' > mounts.txt
```

### 4. Dump
```bash
sudo env LD_LIBRARY_PATH=$LD_LIBRARY_PATH \
  criu dump --tree $PID \
            --images-dir /var/lib/kubernetes/checkpoints/pytorch \
            --tcp-established --ext-unix-sk --shell-job \
            --enable-fs hugetlbfs --enable-external-masters \
            --lib /usr/local/lib/criu \
            $(cat mounts.txt)
```

### 5. Restore (manuell)
```bash
sudo env LD_LIBRARY_PATH=$LD_LIBRARY_PATH \
  criu restore --images-dir /var/lib/kubernetes/checkpoints/pytorch \
               --shell-job --restore-detached --lib /usr/local/lib/criu
```

### 6. Ergebnisse notieren
*   Dauer Dump/Restore
*   Notwendige Privilegien
*   Probleme mit cgroups / Namespace

## Erwartung
Wenn dieser Test erfolgreich ist, wissen wir, dass der NVIDIA-Runtime-Pod prinzipiell checkpointbar ist – das ist die Voraussetzung, um den Shim später damit zu verdrahten.
