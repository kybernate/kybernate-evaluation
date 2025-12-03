#!/bin/bash
# GPU Checkpoint Script for Task 06
# Creates a checkpoint of a running GPU container using containerd and CRIU

set -e

# Configuration
NAMESPACE="kybernate-system"
POD_NAME="${1:-gpu-test}"
CTR="/snap/microk8s/current/bin/ctr"
CTR_ARGS="--namespace k8s.io --address /var/snap/microk8s/common/run/containerd.sock"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if CRIU CUDA plugin exists
    if [[ ! -f /usr/local/lib/criu/cuda_plugin.so ]]; then
        log_error "CRIU CUDA plugin not found at /usr/local/lib/criu/cuda_plugin.so"
        exit 1
    fi
    
    # Check if pod is running
    if ! microk8s kubectl get pod "$POD_NAME" -n "$NAMESPACE" &>/dev/null; then
        log_error "Pod $POD_NAME not found in namespace $NAMESPACE"
        exit 1
    fi
    
    POD_STATUS=$(microk8s kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}')
    if [[ "$POD_STATUS" != "Running" ]]; then
        log_error "Pod $POD_NAME is not running (status: $POD_STATUS)"
        exit 1
    fi
    
    log_info "Prerequisites OK"
}

# Find container ID
find_container_id() {
    log_info "Finding container ID for pod $POD_NAME..."
    
    # Get container ID (exclude pause container)
    CONTAINER_ID=$(sudo $CTR $CTR_ARGS containers list 2>/dev/null | grep pytorch | grep -v pause | awk '{print $1}' | head -1)
    
    if [[ -z "$CONTAINER_ID" ]]; then
        log_error "Could not find pytorch container"
        exit 1
    fi
    
    log_info "Container ID: $CONTAINER_ID"
}

# Get current counter value
get_counter_value() {
    log_info "Current counter value:"
    microk8s kubectl logs "$POD_NAME" -n "$NAMESPACE" --tail=3
}

# Create checkpoint
create_checkpoint() {
    log_info "Creating GPU checkpoint..."
    
    TIMESTAMP=$(date +%Y%m%d-%H%M%S)
    
    # Create checkpoint using containerd with CRIU CUDA plugin
    if sudo CRIU_LIBS=/usr/local/lib/criu $CTR $CTR_ARGS tasks checkpoint "$CONTAINER_ID" \
        --checkpoint-path /tmp/gpu-checkpoint 2>&1; then
        log_info "Checkpoint created successfully"
    else
        log_error "Checkpoint creation failed"
        exit 1
    fi
    
    # Show checkpoint info
    log_info "Checkpoint stored as containerd image"
    echo ""
    sudo $CTR $CTR_ARGS images ls 2>/dev/null | grep "checkpoint.*$CONTAINER_ID" | head -1
}

# Show checkpoint size
show_checkpoint_info() {
    log_info "Checkpoint details:"
    CHECKPOINT_LINE=$(sudo $CTR $CTR_ARGS images ls 2>/dev/null | grep "checkpoint.*$CONTAINER_ID" | tail -1)
    CHECKPOINT_SIZE=$(echo "$CHECKPOINT_LINE" | awk '{print $5}')
    echo "  Size: $CHECKPOINT_SIZE"
    
    # Check if size indicates GPU memory was captured
    if [[ "$CHECKPOINT_SIZE" == *"MiB"* ]]; then
        SIZE_NUM=$(echo "$CHECKPOINT_SIZE" | grep -oP '[\d.]+')
        if (( $(echo "$SIZE_NUM > 100" | bc -l) )); then
            log_info "Checkpoint includes GPU memory (size > 100 MiB)"
        fi
    elif [[ "$CHECKPOINT_SIZE" == *"GiB"* ]]; then
        log_info "Checkpoint includes GPU memory (size in GiB)"
    fi
}

# Main
main() {
    echo "=================================="
    echo "  GPU Checkpoint Script (Task 06)"
    echo "=================================="
    echo ""
    
    check_prerequisites
    find_container_id
    echo ""
    get_counter_value
    echo ""
    create_checkpoint
    echo ""
    show_checkpoint_info
    
    echo ""
    log_info "GPU checkpoint completed successfully!"
    echo ""
    echo "Pod continues running after checkpoint."
    echo "Use gpu-restore.sh to restore from this checkpoint."
}

main "$@"
