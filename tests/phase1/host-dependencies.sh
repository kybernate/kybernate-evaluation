#!/usr/bin/env bash
# Phase 1: Host-Abhängigkeiten Validierung
# Prüft alle Systemvoraussetzungen für Kybernate

set -uo pipefail
# Kein set -e, da wir Fehler selbst behandeln

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$SCRIPT_DIR/../lib/test-utils.sh"

header "Phase 1: Host-Abhängigkeiten"

# ============================================
# System & Kernel
# ============================================
subheader "System & Kernel"

# Kernel Version
KERNEL_VERSION=$(uname -r)
KERNEL_MAJOR=$(echo "$KERNEL_VERSION" | cut -d. -f1)
if [[ "$KERNEL_MAJOR" -ge 5 ]]; then
    pass "Kernel: $KERNEL_VERSION"
else
    fail "Kernel zu alt: $KERNEL_VERSION (benötigt 5.x+)"
fi

# Cgroups v2
if [[ -f /sys/fs/cgroup/cgroup.controllers ]]; then
    pass "Cgroups v2 aktiviert"
else
    warn "Cgroups v2 nicht gefunden (legacy mode?)"
fi

# ============================================
# NVIDIA Stack
# ============================================
subheader "NVIDIA Stack"

# NVIDIA Treiber
if command_exists nvidia-smi && nvidia-smi &>/dev/null; then
    DRIVER_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1)
    GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -1)
    pass "NVIDIA Driver: $DRIVER_VERSION"
    info "GPU: $GPU_NAME"
else
    fail "NVIDIA Driver nicht gefunden"
fi

# CUDA Toolkit
if command_exists nvcc; then
    CUDA_VERSION=$(nvcc --version | grep "release" | awk '{print $6}' | tr -d ',')
    pass "CUDA Toolkit: $CUDA_VERSION"
else
    warn "CUDA Toolkit nicht im PATH"
fi

# cuda-checkpoint
if command_exists cuda-checkpoint; then
    pass "cuda-checkpoint verfügbar"
else
    warn "cuda-checkpoint nicht gefunden (benötigt für GPU-Checkpoint)"
fi

# NVIDIA Container Toolkit
if command_exists nvidia-ctk; then
    CTK_VERSION=$(nvidia-ctk --version 2>/dev/null | head -1 || echo "unbekannt")
    pass "NVIDIA Container Toolkit: $CTK_VERSION"
else
    warn "nvidia-ctk nicht gefunden"
fi

# nvidia-container-runtime
if command_exists nvidia-container-runtime; then
    pass "nvidia-container-runtime verfügbar"
else
    warn "nvidia-container-runtime nicht gefunden (benötigt für GPU-Pods)"
fi

# ============================================
# Checkpointing Tools
# ============================================
subheader "Checkpointing Tools"

# CRIU
if command_exists criu; then
    CRIU_VERSION=$(criu --version 2>&1 | head -1)
    pass "CRIU: $CRIU_VERSION"
else
    fail "CRIU nicht gefunden"
fi

# CRIU Check (als root)
if is_root; then
    if criu check &>/dev/null; then
        pass "CRIU check passed"
    else
        warn "CRIU check fehlgeschlagen (einige Features evtl. nicht verfügbar)"
    fi
else
    info "CRIU check übersprungen (benötigt root)"
fi

# CRIU CUDA Plugin
if [[ -f /usr/local/lib/criu/cuda_plugin.so ]]; then
    pass "CRIU CUDA Plugin: /usr/local/lib/criu/cuda_plugin.so"
else
    warn "CRIU CUDA Plugin nicht gefunden (benötigt für GPU-Checkpoint)"
    info "Erwartet unter: /usr/local/lib/criu/cuda_plugin.so"
fi

# ============================================
# Container Runtime
# ============================================
subheader "Container Runtime"

# Docker
if command_exists docker; then
    DOCKER_VERSION=$(docker --version | awk '{print $3}' | tr -d ',')
    pass "Docker: $DOCKER_VERSION"
else
    warn "Docker nicht gefunden"
fi

# Containerd (via MicroK8s)
if command_succeeds microk8s ctr version; then
    pass "Containerd (MicroK8s)"
else
    warn "Containerd nicht erreichbar"
fi

# crictl
if command_exists crictl; then
    CRICTL_VERSION=$(crictl --version 2>/dev/null | awk '{print $3}')
    pass "crictl: $CRICTL_VERSION"
else
    warn "crictl nicht gefunden"
fi

# ============================================
# Kubernetes (MicroK8s)
# ============================================
subheader "Kubernetes (MicroK8s)"

# MicroK8s Status
if command_succeeds microk8s status --wait-ready; then
    pass "MicroK8s läuft"
else
    fail "MicroK8s nicht aktiv"
fi

# Kubernetes Version
K8S_VERSION=$(microk8s kubectl version 2>/dev/null | grep "Server Version" | awk '{print $3}' || echo "")
if [[ -n "$K8S_VERSION" ]]; then
    pass "Kubernetes: $K8S_VERSION"
else
    warn "K8s Version nicht ermittelbar"
fi

# GPU allocatable
if microk8s kubectl get nodes -o jsonpath='{.items[*].status.allocatable}' 2>/dev/null | grep -q "nvidia.com/gpu"; then
    GPU_COUNT=$(microk8s kubectl get nodes -o jsonpath='{.items[*].status.allocatable.nvidia\.com/gpu}' 2>/dev/null)
    pass "GPUs allocatable: $GPU_COUNT"
else
    warn "Keine GPUs im Cluster allocatable"
fi

# RuntimeClass kybernate
if command_succeeds microk8s kubectl get runtimeclass kybernate; then
    pass "RuntimeClass 'kybernate' vorhanden"
else
    warn "RuntimeClass 'kybernate' nicht gefunden"
fi

# Namespace kybernate-system
if command_succeeds microk8s kubectl get namespace kybernate-system; then
    pass "Namespace 'kybernate-system' vorhanden"
else
    warn "Namespace 'kybernate-system' nicht gefunden"
fi

# ============================================
# Entwicklungsumgebung
# ============================================
subheader "Entwicklungsumgebung"

# Go
if command_exists go; then
    GO_VERSION=$(go version | awk '{print $3}')
    pass "Go: $GO_VERSION"
else
    fail "Go nicht gefunden"
fi

# Protobuf Compiler
if command_exists protoc; then
    PROTOC_VERSION=$(protoc --version | awk '{print $2}')
    pass "protoc: $PROTOC_VERSION"
else
    warn "protoc nicht gefunden"
fi

# Go Protobuf Plugins
if command_exists protoc-gen-go; then
    pass "protoc-gen-go verfügbar"
else
    warn "protoc-gen-go nicht gefunden"
fi

# ============================================
# Kybernate Shim
# ============================================
subheader "Kybernate Shim"

# Shim Binary
SHIM_PATH="/usr/local/bin/containerd-shim-kybernate-v1"
if [[ -f "$SHIM_PATH" ]]; then
    pass "Shim Binary: $SHIM_PATH"
else
    warn "Shim nicht installiert: $SHIM_PATH"
fi

# Containerd Konfiguration
CONTAINERD_CONFIG="/var/snap/microk8s/current/args/containerd-template.toml"
if [[ -f "$CONTAINERD_CONFIG" ]] && grep -q "runtimes.kybernate" "$CONTAINERD_CONFIG" 2>/dev/null; then
    pass "Containerd konfiguriert für kybernate Runtime"
else
    warn "Containerd nicht für kybernate Runtime konfiguriert"
fi

# Shim Log
if [[ -f /tmp/kybernate-shim.log ]]; then
    LOG_LINES=$(wc -l < /tmp/kybernate-shim.log)
    pass "Shim-Log vorhanden ($LOG_LINES Zeilen)"
else
    info "Kein Shim-Log vorhanden (wird beim ersten Aufruf erstellt)"
fi

# ============================================
# Zusammenfassung
# ============================================
summary "Host-Abhängigkeiten"
