import datetime
import os
import time
import torch
import sys

def log(msg: str) -> None:
    print(f"[{datetime.datetime.now().isoformat()}] {msg}", flush=True)

def main() -> None:
    log(f"Starte GPU Stress Runner (PID={os.getpid()})")
    
    if not torch.cuda.is_available():
        log("FATAL: Kein CUDA verfügbar! Beende.")
        sys.exit(1)

    try:
        device = torch.device("cuda:0")
        log(f"Nutze Device: {torch.cuda.get_device_name(0)}")

        # ~2GB VRAM mit float32 (4 bytes * 500M = 2GB)
        num_elements = 500 * 1000 * 1000  
        log(f"Alloziere Tensor mit {num_elements} Elementen (~2GB)")

        tensor_a = torch.ones(num_elements, device=device)
        tensor_b = torch.tensor([1.0], device=device)
        log("Allokation erfolgreich")

        counter = 0
        while True:
            # Einfache Berechnung um GPU aktiv zu halten
            tensor_a.add_(tensor_b) 
            
            if counter % 5 == 0:
                torch.cuda.synchronize()
                val = float(tensor_a[0])
                mem_mb = torch.cuda.memory_allocated(0) / 1024 / 1024
                log(f"Loop {counter}: Wert={val:.1f}, VRAM={mem_mb:.2f} MB")
            
            counter += 1
            time.sleep(1)
            
    except Exception as e:
        log(f"ERROR: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()
