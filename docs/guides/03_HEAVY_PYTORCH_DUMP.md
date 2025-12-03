# Dump a heavy PyTorch workload

## Create the Test Pod

```
cd ~
mkdir -p pytorch-test
cd pytorch-test
```

Create pytorch test

```
cat <<'EOF' > stress_vram.py
import torch
import time
import os
import datetime

def log(msg):
    # flush=True ist wichtig für Docker Logs
    print(f"[{datetime.datetime.now()}] {msg}", flush=True)

def main():
    log(f"Starte PyTorch VRAM Stress Test (PID: {os.getpid()})")
    
    if not torch.cuda.is_available():
        log("FEHLER: Kein CUDA gefunden!")
        return

    device = torch.device("cuda:0")
    log(f"Nutze Device: {torch.cuda.get_device_name(0)}")

    # Wir allozieren ca 2 GB VRAM (Float32 = 4 Bytes)
    # 500 Mio Elemente * 4 Bytes = ~2 GB
    num_elements = 500 * 1000 * 1000 
    
    log(f"Alloziere Tensor mit {num_elements} Elementen (~2GB)...")
    try:
        # Großer Tensor im VRAM
        tensor_a = torch.ones(num_elements, device=device)
        # Kleiner Tensor für Berechnungen
        tensor_b = torch.tensor([1.0], device=device)
        
        log("Allokation erfolgreich.")
    except Exception as e:
        log(f"Fehler bei Allokation: {e}")
        return

    counter = 0
    while True:
        # Wir machen eine Berechnung auf der GPU, damit der Context aktiv/dirty bleibt
        tensor_a.add_(tensor_b)
        
        # Alle 5 Sekunden Status ausgeben
        if counter % 5 == 0:
            # Synchronisieren, um sicherzugehen, dass GPU fertig ist
            torch.cuda.synchronize()
            # Wir lesen den ersten Wert (sollte steigen: 1, 2, 3...)
            val = tensor_a[0].item()
            mem_alloc = torch.cuda.memory_allocated(0) / 1024 / 1024
            log(f"Loop {counter}: Wert={val:.1f}, VRAM belegt={mem_alloc:.2f} MB")
        
        counter += 1
        time.sleep(1)

if __name__ == "__main__":
    main()
EOF
```

Create Dockerfile

```
cat <<'EOF' > Dockerfile
# Offizielles PyTorch Image als Basis
FROM pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime

WORKDIR /app

# Skript kopieren
COPY stress_vram.py .

# Befehl: Unbuffered output (-u) ist wichtig damit Logs sofort erscheinen
CMD ["python3", "-u", "stress_vram.py"]
EOF
```

Build the container and push to registry

```
sudo docker build -t localhost:32000/gpu-pytorch:v1 .
sudo docker save localhost:32000/gpu-pytorch:v1 > gpu-pytorch.tar
microk8s ctr image import gpu-pytorch.tar
```

Create the pod

```
microk8s kubectl delete pod pytorch-stress --force --grace-period=0 2>/dev/null

microk8s kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: pytorch-stress
spec:
  restartPolicy: Never
  runtimeClassName: nvidia
  containers:
    - name: stresser
      image: localhost:32000/gpu-pytorch:v1
      imagePullPolicy: Never
      resources:
        limits:
          nvidia.com/gpu: 1
EOF
```

Check the pod

```
microk8s kubectl describe pod pytorch-stress
microk8s kubectl logs pytorch-stress -f
```

## Dump The Pod

```
# 1. Find Container ID (The "Grepping" method that worked)
echo "--> Searching for container..."
CONTAINER_ID=$(crictl ps -a | grep "pytorch-stress" | grep "stresser" | awk '{print $1}' | head -n 1)

if [ -z "$CONTAINER_ID" ]; then
    echo "❌ Container not found. Is the pod running?"
fi
echo "✅ Container ID: $CONTAINER_ID"

# 2. Find Host PID (Using jq to safely get the HOST-PID, not the container-PID 1)
# If jq is missing: sudo apt install jq -y
echo "--> Determining Host PID..."
CONTAINER_PID=$(crictl inspect $CONTAINER_ID | jq '.info.pid')

if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" == "null" ]; then
    echo "❌ Could not determine PID."
fi
echo "✅ Host PID: $CONTAINER_PID"

# 3. Generate Ignore List
echo "--> Generating ignore list for NVIDIA & K8s Mounts..."
IGNORE_MOUNTS=$(sudo cat /proc/$CONTAINER_PID/mountinfo | grep -E "nvidia|etc/hosts|resolv.conf|etc/hostname" | awk '{print "--external mnt[" $1 "]:" $5}' | tr '\n' ' ')

# We save this for later (Restore)
echo "$IGNORE_MOUNTS" > mounts.txt
echo "✅ Mounts saved ($(echo $IGNORE_MOUNTS | wc -w) entries)."

# 4. Set Environment
export CUDA_HOME=/usr/local/cuda
export LD_LIBRARY_PATH=$CUDA_HOME/lib64:$LD_LIBRARY_PATH
PLUGIN_DIR=/usr/local/lib/criu

# 5. Prepare Directory
mkdir -p pytorch_dump
sudo chown $USER:$USER pytorch_dump
rm -rf pytorch_dump/*

echo "--> Starting DUMP..."

if sudo env LD_LIBRARY_PATH=$LD_LIBRARY_PATH criu dump --tree $CONTAINER_PID \
     --images-dir pytorch_dump \
     --tcp-established \
     --ext-unix-sk \
     --shell-job \
     --lib $PLUGIN_DIR \
     --enable-fs hugetlbfs \
     --enable-external-masters \
     --ext-mount-map auto \
     $IGNORE_MOUNTS \
     -v4 -o dump.log; then
     
    echo "🎉🎉🎉 DUMP SUCCESSFUL! 🎉🎉🎉"
    echo "The process was stopped. Data is located in ./pytorch_dump"
else
    echo "❌ DUMP FAILED"
    echo "Log excerpt:"
    sudo grep -iE "error|warn" -C 2 pytorch_dump/dump.log | tail -n 10
fi
```