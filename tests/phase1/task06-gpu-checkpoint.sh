#!/usr/bin/env bash
# Phase 1 - Task 06: GPU Checkpoint E2E Test
# Testet den kompletten Checkpoint/Restore Workflow für GPU-Workloads (CUDA)

set -uo pipefail
# Kein set -e, da wir Fehler selbst behandeln

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
source "$SCRIPT_DIR/../lib/test-utils.sh"

# Konfiguration
NAMESPACE="kybernate-system"
TEST_POD="gpu-test-e2e"
RESTORE_POD="gpu-restore-e2e"
CHECKPOINT_PATH="/tmp/kybernate-checkpoint"
GPU_IMAGE="localhost:32000/gpu-pytorch:v1"
WAIT_SECONDS=20  # Zeit für GPU-Initialisierung und Counter

header "Phase 1 - Task 06: GPU Checkpoint/Restore Test"

# ============================================
# Voraussetzungen prüfen
# ============================================
subheader "Voraussetzungen"

# MicroK8s läuft?
if command_succeeds microk8s status --wait-ready; then
    pass "MicroK8s läuft"
else
    fail "MicroK8s nicht verfügbar"
    exit 1
fi

# GPU verfügbar?
if nvidia-smi &>/dev/null; then
    GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)
    pass "GPU verfügbar: $GPU_NAME"
else
    fail "Keine GPU gefunden"
    exit 1
fi

# GPU im Cluster allocatable?
if microk8s kubectl get nodes -o jsonpath='{.items[*].status.allocatable}' 2>/dev/null | grep -q "nvidia.com/gpu"; then
    GPU_COUNT=$(microk8s kubectl get nodes -o jsonpath='{.items[*].status.allocatable.nvidia\.com/gpu}' 2>/dev/null)
    pass "GPUs im Cluster: $GPU_COUNT"
else
    fail "Keine GPUs im Cluster allocatable"
    exit 1
fi

# RuntimeClass vorhanden?
if command_succeeds microk8s kubectl get runtimeclass kybernate; then
    pass "RuntimeClass 'kybernate' vorhanden"
else
    fail "RuntimeClass 'kybernate' nicht vorhanden"
    exit 1
fi

# Namespace vorhanden?
if command_succeeds microk8s kubectl get namespace "$NAMESPACE"; then
    pass "Namespace '$NAMESPACE' vorhanden"
else
    microk8s kubectl create namespace "$NAMESPACE"
    pass "Namespace erstellt"
fi

# GPU-Image vorhanden?
# Use awk to extract the first column and check for exact match to avoid grep issues with whitespace
if sudo microk8s ctr images list | awk '{print $1}' | grep -q "^localhost:32000/gpu-pytorch:v1$"; then
    pass "GPU-Image gefunden"
else
    # Fallback check without tag or with different format
    if sudo microk8s ctr images list | grep -q "gpu-pytorch"; then
         pass "GPU-Image gefunden (fuzzy match)"
    else
        fail "GPU-Image nicht gefunden: $GPU_IMAGE"
        info "Verfügbare Images:"
        sudo microk8s ctr images list | grep gpu-pytorch || true
        info "Bitte zuerst bauen:"
        info "  cd $PROJECT_ROOT/phases/phase1/task03-k8s-gpu-checkpoint/workspace"
        info "  sudo docker build -t $GPU_IMAGE ."
        info "  sudo docker push $GPU_IMAGE"
        # exit 1
    fi
fi

# CRIU CUDA Plugin?
if [[ -f /usr/local/lib/criu/cuda_plugin.so ]]; then
    pass "CRIU CUDA Plugin vorhanden"
else
    fail "CRIU CUDA Plugin nicht gefunden"
    exit 1
fi

# ============================================
# Aufräumen
# ============================================
subheader "Cleanup"

cleanup_pod "$TEST_POD" "$NAMESPACE"
cleanup_pod "$RESTORE_POD" "$NAMESPACE"
sudo rm -rf "$CHECKPOINT_PATH" 2>/dev/null || true

pass "Alte Ressourcen bereinigt"

# ============================================
# GPU Test-Pod starten
# ============================================
subheader "GPU Test-Pod starten"

cat <<EOF | microk8s kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $TEST_POD
  namespace: $NAMESPACE
spec:
  runtimeClassName: kybernate
  containers:
  - name: pytorch
    image: $GPU_IMAGE
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

info "Warte auf GPU-Pod (kann länger dauern)..."
if wait_for_pod "$TEST_POD" "$NAMESPACE" 180; then
    pass "GPU Test-Pod gestartet"
else
    fail "GPU Test-Pod konnte nicht gestartet werden"
    microk8s kubectl describe pod "$TEST_POD" -n "$NAMESPACE"
    exit 1
fi

# VRAM-Belegung prüfen
sleep 5
VRAM_USED=$(nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits | head -1)
info "VRAM belegt: ${VRAM_USED}MB"

# Warten bis Counter hochzählt
info "Warte $WAIT_SECONDS Sekunden für GPU-Initialisierung und Counter..."
sleep "$WAIT_SECONDS"

# Counter-Stand vor Checkpoint
COUNTER_BEFORE=$(microk8s kubectl logs "$TEST_POD" -n "$NAMESPACE" --tail=1 | grep -oP 'Iteration \K[0-9]+' || echo "0")
pass "Counter vor Checkpoint: $COUNTER_BEFORE"

# ============================================
# GPU Checkpoint erstellen
# ============================================
subheader "GPU Checkpoint erstellen"

# Container-ID ermitteln
CONTAINER_ID=$(get_container_id "pytorch" "$NAMESPACE")

if [[ -z "$CONTAINER_ID" ]]; then
    fail "Container-ID konnte nicht ermittelt werden"
    exit 1
fi
pass "Container-ID: ${CONTAINER_ID:0:12}..."

# DEBUG: Mountinfo ausgeben
PID=$(sudo microk8s ctr --namespace k8s.io tasks list | grep ${CONTAINER_ID} | awk '{print $2}')
if [[ -n "$PID" ]]; then
    info "Container PID: $PID"
    info "Mountinfo vor Checkpoint:"
    sudo cat /proc/$PID/mountinfo
fi

# GPU Checkpoint ausführen
# Hinweis: Erfordert CRIU mit CUDA-Plugin
sudo mkdir -p /tmp/gpu-checkpoint-work
if sudo microk8s ctr --namespace k8s.io task checkpoint "$CONTAINER_ID" \
    --image-path "$CHECKPOINT_PATH" \
    --work-path /tmp/gpu-checkpoint-work &>/dev/null; then
    pass "GPU Checkpoint-Befehl erfolgreich"
else
    fail "GPU Checkpoint-Befehl fehlgeschlagen"
    info "Dies kann bedeuten:"
    info "  - CRIU CUDA Plugin nicht korrekt geladen"
    info "  - GPU-State nicht checkpoint-fähig"
    info "  - Shim muss für GPU erweitert werden"
    exit 1
fi

# Prüfen ob Checkpoint erstellt wurde
sleep 2
if [[ -d "$CHECKPOINT_PATH" ]]; then
    CHECKPOINT_SIZE=$(du -sh "$CHECKPOINT_PATH" | awk '{print $1}')
    pass "GPU Checkpoint erstellt: $CHECKPOINT_PATH ($CHECKPOINT_SIZE)"
    
    # Prüfen auf CUDA-spezifische Dateien
    if ls "$CHECKPOINT_PATH"/*.cuda 2>/dev/null || ls "$CHECKPOINT_PATH"/cuda-* 2>/dev/null; then
        pass "CUDA-State im Checkpoint enthalten"
    else
        warn "Keine expliziten CUDA-Dateien gefunden (evtl. in pages-*.img)"
    fi
else
    fail "GPU Checkpoint nicht gefunden unter $CHECKPOINT_PATH"
    exit 1
fi

# ============================================
# Original-Pod löschen
# ============================================
subheader "Original-Pod löschen"

microk8s kubectl delete pod "$TEST_POD" -n "$NAMESPACE" --force --grace-period=0 &>/dev/null
sleep 2

# VRAM sollte jetzt frei sein
VRAM_AFTER_DELETE=$(nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits | head -1)
info "VRAM nach Pod-Löschung: ${VRAM_AFTER_DELETE}MB"
pass "Original-Pod gelöscht"

# ============================================
# GPU Restore-Pod starten
# ============================================
subheader "GPU Restore-Pod starten"

cat <<EOF | microk8s kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $RESTORE_POD
  namespace: $NAMESPACE
spec:
  runtimeClassName: kybernate
  containers:
  - name: pytorch
    image: $GPU_IMAGE
    command: ["sleep", "infinity"]
    env:
    - name: RESTORE_FROM
      value: "$CHECKPOINT_PATH"
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

if wait_for_pod "$RESTORE_POD" "$NAMESPACE" 120; then
    pass "GPU Restore-Pod gestartet"
else
    fail "GPU Restore-Pod konnte nicht gestartet werden"
    microk8s kubectl describe pod "$RESTORE_POD" -n "$NAMESPACE"
    exit 1
fi

# Warten auf Restore-Stabilisierung
sleep 5

# ============================================
# GPU Restore verifizieren
# ============================================
subheader "GPU Restore verifizieren"

# VRAM-Belegung prüfen
VRAM_RESTORED=$(nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits | head -1)
info "VRAM nach Restore: ${VRAM_RESTORED}MB"

if [[ "$VRAM_RESTORED" -gt "$VRAM_AFTER_DELETE" ]]; then
    pass "VRAM wieder belegt (GPU-State wiederhergestellt)"
else
    warn "VRAM-Belegung nicht wie erwartet gestiegen"
fi

# Counter-Stand nach Restore
COUNTER_AFTER=$(microk8s kubectl logs "$RESTORE_POD" -n "$NAMESPACE" --tail=1 2>/dev/null | grep -oP 'Iteration \K[0-9]+' || echo "0")

if [[ "$COUNTER_AFTER" -ge "$COUNTER_BEFORE" ]]; then
    pass "Counter fortgesetzt: $COUNTER_BEFORE → $COUNTER_AFTER"
else
    if [[ "$COUNTER_AFTER" -eq 0 ]]; then
        fail "Counter bei 0 gestartet (Restore fehlgeschlagen)"
        info "Logs:"
        microk8s kubectl logs "$RESTORE_POD" -n "$NAMESPACE" --tail=10 || true
    else
        warn "Counter-Wert unerwartet: vor=$COUNTER_BEFORE, nach=$COUNTER_AFTER"
    fi
fi

# ============================================
# Aufräumen
# ============================================
subheader "Aufräumen"

cleanup_pod "$RESTORE_POD" "$NAMESPACE"
pass "Test-Pods bereinigt"

# ============================================
# Zusammenfassung
# ============================================
summary "GPU Checkpoint/Restore (Task 06)"
