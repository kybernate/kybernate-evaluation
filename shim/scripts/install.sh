#!/usr/bin/env bash
set -euo pipefail

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(dirname "$SCRIPT_DIR")
BIN_NAME="containerd-shim-kybernate-v1"
INSTALL_PATH="/usr/local/bin/$BIN_NAME"

log() {
    echo -e "${GREEN}[Kybernate Installer]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[Warning]${NC} $1"
}

if [[ $EUID -ne 0 ]]; then
   warn "This script must be run as root"
   exit 1
fi

# 1. Build
if command -v go &> /dev/null; then
    log "Building shim binary..."
    cd "$ROOT_DIR"
    go build -o "bin/$BIN_NAME" "./cmd/$BIN_NAME"
else
    warn "Go not found. Assuming binary is already built in bin/"
fi

if [[ ! -f "$ROOT_DIR/bin/$BIN_NAME" ]]; then
    echo "Error: Binary not found at $ROOT_DIR/bin/$BIN_NAME"
    exit 1
fi

# 2. Install Binary
log "Installing binary to $INSTALL_PATH..."
cp "$ROOT_DIR/bin/$BIN_NAME" "$INSTALL_PATH"
chmod +x "$INSTALL_PATH"

# 3. Configure Containerd
RUNTIME_CONFIG='
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate]
      runtime_type = "io.containerd.kybernate.v1"
'

configure_microk8s() {
    CONFIG_FILE="/var/snap/microk8s/current/args/containerd-template.toml"
    if [[ -f "$CONFIG_FILE" ]]; then
        log "Detected MicroK8s. Configuring $CONFIG_FILE..."
        
        if grep -q "runtimes.kybernate" "$CONFIG_FILE"; then
            log "Configuration already exists in MicroK8s."
        else
            # Insert before the CNI config section as a safe anchor
            # This is a heuristic; might need adjustment based on specific toml structure
            sed -i '/\[plugins."io.containerd.grpc.v1.cri".cni\]/i \
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate]\
      runtime_type = "io.containerd.kybernate.v1"\
' "$CONFIG_FILE"
            log "Configuration applied. Restarting MicroK8s..."
            microk8s stop && microk8s start
        fi
    fi
}

configure_standard_containerd() {
    CONFIG_FILE="/etc/containerd/config.toml"
    if [[ -f "$CONFIG_FILE" ]]; then
        log "Detected Standard Containerd. Configuring $CONFIG_FILE..."
        
        if grep -q "runtimes.kybernate" "$CONFIG_FILE"; then
            log "Configuration already exists."
        else
            # Append to the end of runtimes section or file
            # This is tricky with TOML. We try to find the runtimes section.
            # If we can't parse TOML reliably with bash, we append a warning or try a simple insertion.
            
            # Simple approach: Check if we can insert after runc
            if grep -q 'runtimes.runc' "$CONFIG_FILE"; then
                 sed -i '/\[.*runtimes.runc\]/a \
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate]\
        runtime_type = "io.containerd.kybernate.v1"' "$CONFIG_FILE"
            else
                warn "Could not find 'runtimes.runc' anchor. Please add the configuration manually:"
                echo "$RUNTIME_CONFIG"
            fi
            
            log "Restarting containerd..."
            systemctl restart containerd
        fi
    fi
}

# Detect environment
if command -v microk8s &> /dev/null; then
    configure_microk8s
elif [[ -f "/etc/containerd/config.toml" ]]; then
    configure_standard_containerd
else
    warn "No supported containerd configuration found (MicroK8s or /etc/containerd/config.toml)."
    warn "Please configure containerd manually to use runtime_type = 'io.containerd.kybernate.v1'"
fi

log "Installation complete!"
