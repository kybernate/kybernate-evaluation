#!/usr/bin/env bash
set -u

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BIN_DIR="$ROOT_DIR/bin"
MANIFESTS_DIR="$ROOT_DIR/shim/manifests"
GPU_MANIFEST="$ROOT_DIR/manifests/gpu-ckpt-test.yaml"
CHECKPOINT_ROOT="/var/lib/kybernate/checkpoints"

# Ensure binaries exist
if [[ ! -f "$BIN_DIR/kybernate-ctl" ]]; then
    echo "Error: kybernate-ctl not found in $BIN_DIR"
    exit 1
fi
if [[ ! -f "$BIN_DIR/kybernate-restore-pod" ]]; then
    echo "Error: kybernate-restore-pod not found in $BIN_DIR"
    exit 1
fi

# Helper to wait for pod
wait_for_pod() {
    local pod=$1
    local ns=$2
    echo "Waiting for pod $pod in $ns to be Running..."
    for i in {1..60}; do
        status=$(microk8s kubectl get pod "$pod" -n "$ns" -o jsonpath='{.status.phase}' 2>/dev/null)
        if [[ "$status" == "Running" ]]; then
            echo "Pod $pod is Running."
            return 0
        fi
        sleep 2
    done
    echo "Timeout waiting for pod $pod"
    return 1
}

# Helper to wait for logs
wait_for_logs() {
    local pod=$1
    local ns=$2
    local pattern=$3
    echo "Waiting for logs in $pod matching '$pattern'..."
    for i in {1..30}; do
        if microk8s kubectl logs "$pod" -n "$ns" 2>/dev/null | grep -q "$pattern"; then
            echo "Found pattern '$pattern' in logs."
            return 0
        fi
        sleep 2
    done
    echo "Timeout waiting for logs"
    return 1
}

echo "========================================================"
echo "TEST 1: CPU Checkpoint & Restore (Method C)"
echo "========================================================"

NS="kybernate-system"
CPU_POD="cpu-test"
CPU_RESTORE_POD="cpu-test-restored"

# Cleanup
microk8s kubectl delete pod "$CPU_POD" -n "$NS" --ignore-not-found --wait=true
microk8s kubectl delete pod "$CPU_RESTORE_POD" -n "$NS" --ignore-not-found --wait=true
sudo rm -rf "$CHECKPOINT_ROOT/$NS/$CPU_POD"

# Deploy CPU Pod
echo "Deploying CPU Pod..."
microk8s kubectl apply -f "$MANIFESTS_DIR/cpu-test-pod.yaml"
wait_for_pod "$CPU_POD" "$NS"

# Wait for some logs
wait_for_logs "$CPU_POD" "$NS" "Counter: 5"

# Checkpoint
echo "Checkpointing CPU Pod..."
sudo "$BIN_DIR/kybernate-ctl" checkpoint -n "$NS" -p "$CPU_POD" -c "counter"

# Find checkpoint path
LATEST_CKPT=$(ls -td "$CHECKPOINT_ROOT/$NS/$CPU_POD/counter/"* | head -1)
echo "Checkpoint created at: $LATEST_CKPT"

# Delete original pod
echo "Deleting original pod..."
microk8s kubectl delete pod "$CPU_POD" -n "$NS" --wait=true

# Generate Restore Manifest
echo "Generating restore manifest..."
"$BIN_DIR/kybernate-restore-pod" \
    --checkpoint "$LATEST_CKPT" \
    --image "python:3.9-slim" \
    --name "$CPU_RESTORE_POD" \
    --namespace "$NS" \
    --gpu=false \
    > /tmp/restore-cpu.yaml

# Apply Restore
echo "Applying restore manifest..."
microk8s kubectl apply -f /tmp/restore-cpu.yaml
wait_for_pod "$CPU_RESTORE_POD" "$NS"

# Verify Logs
echo "Verifying logs..."
# It should continue counting, so we expect numbers > 5
wait_for_logs "$CPU_RESTORE_POD" "$NS" "Counter:"
microk8s kubectl logs "$CPU_RESTORE_POD" -n "$NS" | tail -n 5

echo "CPU Test Passed!"
echo ""

echo "========================================================"
echo "TEST 2: GPU Checkpoint & Restore (Method C)"
echo "========================================================"

run_gpu_test() {
    GPU_POD="gpu-ckpt-test"
    GPU_RESTORE_POD="gpu-ckpt-test-restored"

    # Cleanup
    microk8s kubectl delete pod "$GPU_POD" -n "$NS" --ignore-not-found --wait=true
    microk8s kubectl delete pod "$GPU_RESTORE_POD" -n "$NS" --ignore-not-found --wait=true
    sudo rm -rf "$CHECKPOINT_ROOT/$NS/$GPU_POD"

    # Deploy GPU Pod
    echo "Deploying GPU Pod..."
    # We need to ensure the GPU pod uses kybernate runtime if we want consistency, 
    # but let's try with the existing manifest first.
    # Actually, let's patch the manifest to use kybernate runtime just in case.
    sed 's/runtimeClassName: nvidia/runtimeClassName: kybernate/' "$GPU_MANIFEST" > /tmp/gpu-test-patched.yaml
    microk8s kubectl apply -f /tmp/gpu-test-patched.yaml
    if ! wait_for_pod "$GPU_POD" "$NS"; then
        echo "Failed to start GPU pod. Skipping rest of GPU test."
        return 1
    fi

    # Wait for some logs
    if ! wait_for_logs "$GPU_POD" "$NS" "Loop 3"; then
        echo "Failed to find logs. Skipping rest of GPU test."
        return 1
    fi

    # Checkpoint
    echo "Checkpointing GPU Pod..."
    if ! sudo "$BIN_DIR/kybernate-ctl" checkpoint -n "$NS" -p "$GPU_POD" -c "cuda"; then
        echo "Checkpoint failed."
        return 1
    fi

    # Find checkpoint path
    LATEST_CKPT_GPU=$(ls -td "$CHECKPOINT_ROOT/$NS/$GPU_POD/cuda/"* | head -1)
    if [[ -z "$LATEST_CKPT_GPU" ]]; then
        echo "No checkpoint found."
        return 1
    fi
    echo "Checkpoint created at: $LATEST_CKPT_GPU"

    # Delete original pod
    echo "Deleting original pod..."
    microk8s kubectl delete pod "$GPU_POD" -n "$NS" --wait=true

    # Generate Restore Manifest
    echo "Generating restore manifest..."
    "$BIN_DIR/kybernate-restore-pod" \
        --checkpoint "$LATEST_CKPT_GPU" \
        --image "pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime" \
        --name "$GPU_RESTORE_POD" \
        --namespace "$NS" \
        --gpu=true \
        > /tmp/restore-gpu.yaml

    # Apply Restore
    echo "Applying restore manifest..."
    microk8s kubectl apply -f /tmp/restore-gpu.yaml
    if ! wait_for_pod "$GPU_RESTORE_POD" "$NS"; then
        echo "Restore pod failed to start."
        return 1
    fi

    # Verify Logs
    echo "Verifying logs..."
    # It should continue counting loops
    if ! wait_for_logs "$GPU_RESTORE_POD" "$NS" "Loop"; then
        echo "GPU Restore Failed: Logs not found."
        microk8s kubectl describe pod "$GPU_RESTORE_POD" -n "$NS"
        return 1
    fi
    microk8s kubectl logs "$GPU_RESTORE_POD" -n "$NS" | tail -n 5


    echo "GPU Test Passed!"
    return 0
}

# Check for GPU nodes
if microk8s kubectl describe nodes | grep -q "nvidia.com/gpu"; then
    echo "GPU detected. Running GPU tests..."
    run_gpu_test
else
    echo "WARNING: No GPU nodes detected. Skipping GPU test."
    echo "To run GPU tests, ensure you have a node with NVIDIA GPU and nvidia-device-plugin."
fi

