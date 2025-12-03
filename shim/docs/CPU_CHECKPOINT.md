# Task 05: CPU Checkpoint and Restore

This task verifies that we can checkpoint a simple CPU workload (Python counter) and restore it using the Kybernate shim.

## Prerequisites

*   **CRIU installed on the host**: The shim delegates to `runc`, which uses CRIU internally to perform the checkpoint. Ensure `criu` is in the system PATH.
    
    We recommend installing CRIU from source to ensure compatibility with recent kernel features:
    ```bash
    # Install dependencies
    sudo apt-get install -y libprotobuf-dev libprotobuf-c-dev protobuf-c-compiler protobuf-compiler python3-protobuf libnl-3-dev libnet-dev libcap-dev pkg-config build-essential python3-future

    # Clone and build CRIU
    git clone https://github.com/checkpoint-restore/criu.git
    cd criu
    make
    sudo make install
    
    # Verify installation
    criu --version
    ```

## 1. Deploy the Test Pod

The manifest is located at `manifests/cpu-test-pod.yaml`.

Apply it:
```bash
microk8s kubectl apply -f manifests/cpu-test-pod.yaml
```

Verify it is running and counting:
```bash
microk8s kubectl logs -f cpu-test
```

## 2. Checkpoint the Pod

Find the container ID:
```bash
# Use crictl to find the container ID
CONTAINER_ID=$(sudo crictl --runtime-endpoint unix:///var/snap/microk8s/common/run/containerd.sock ps | grep cpu-test | awk '{print $1}')
FULL_ID=$(sudo crictl --runtime-endpoint unix:///var/snap/microk8s/common/run/containerd.sock inspect $CONTAINER_ID | grep '"id":' | head -1 | awk -F '"' '{print $4}')
echo "Container ID: $FULL_ID"
```

Trigger checkpoint using `ctr`:
```bash
sudo mkdir -p /tmp/checkpoint /tmp/checkpoint-work
sudo microk8s ctr --namespace k8s.io task checkpoint --image-path /tmp/checkpoint --work-path /tmp/checkpoint-work $FULL_ID
```

The shim will intercept this and copy the checkpoint to `/tmp/kybernate-checkpoint`.
Verify files exist:
```bash
sudo ls -l /tmp/kybernate-checkpoint
```

## 3. Delete the original Pod

Now that we have the checkpoint, delete the original pod to verify that the restore brings it back.

```bash
microk8s kubectl delete pod cpu-test --force --grace-period=0
```

## 4. Restore the Pod

The manifest is located at `manifests/cpu-restore-pod.yaml`.

Apply it:
```bash
microk8s kubectl apply -f manifests/cpu-restore-pod.yaml
```

Verify it restored and continued counting:
```bash
microk8s kubectl logs -f cpu-restore
```
You should see the counter continuing from where it left off.
