#!/usr/bin/env bash
# Phase 1 - Task 05: CPU Checkpoint E2E Test
# Testet den kompletten Checkpoint/Restore Workflow für CPU-Workloads

set -uo pipefail
# Kein set -e, da wir Fehler selbst behandeln

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
source "$SCRIPT_DIR/../lib/test-utils.sh"

# Konfiguration
NAMESPACE="kybernate-system"
TEST_POD="cpu-test-e2e"
RESTORE_POD="cpu-restore-e2e"
CHECKPOINT_PATH="/tmp/kybernate-checkpoint"
MANIFESTS_DIR="$PROJECT_ROOT/shim/manifests"
WAIT_SECONDS=15  # Zeit zum Hochzählen vor Checkpoint

header "Phase 1 - Task 05: CPU Checkpoint/Restore Test"

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

# RuntimeClass vorhanden?
if command_succeeds microk8s kubectl get runtimeclass kybernate; then
    pass "RuntimeClass 'kybernate' vorhanden"
else
    warn "RuntimeClass nicht vorhanden - erstelle..."
    microk8s kubectl apply -f "$MANIFESTS_DIR/runtimeclass.yaml"
    pass "RuntimeClass erstellt"
fi

# Namespace vorhanden?
if command_succeeds microk8s kubectl get namespace "$NAMESPACE"; then
    pass "Namespace '$NAMESPACE' vorhanden"
else
    warn "Namespace nicht vorhanden - erstelle..."
    microk8s kubectl create namespace "$NAMESPACE"
    pass "Namespace erstellt"
fi

# Shim installiert?
if [[ -f /usr/local/bin/containerd-shim-kybernate-v1 ]]; then
    pass "Kybernate Shim installiert"
else
    fail "Kybernate Shim nicht installiert"
    exit 1
fi

# ============================================
# Aufräumen
# ============================================
subheader "Cleanup"

cleanup_pod "$TEST_POD" "$NAMESPACE"
cleanup_pod "$RESTORE_POD" "$NAMESPACE"
sudo rm -rf "$CHECKPOINT_PATH" 2>/dev/null || true
sudo rm -f /tmp/kybernate-shim.log 2>/dev/null || true

pass "Alte Ressourcen bereinigt"

# ============================================
# Test-Pod starten
# ============================================
subheader "Test-Pod starten"

cat <<EOF | microk8s kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $TEST_POD
  namespace: $NAMESPACE
spec:
  runtimeClassName: kybernate
  containers:
  - name: counter
    image: python:3.9-slim
    command: ["python3", "-c", "import time, os; i=0;\nwhile True:\n  print(f'Counter: {i} | PID: {os.getpid()}', flush=True)\n  i+=1\n  time.sleep(1)"]
EOF

if wait_for_pod "$TEST_POD" "$NAMESPACE" 120; then
    pass "Test-Pod gestartet"
else
    fail "Test-Pod konnte nicht gestartet werden"
    microk8s kubectl describe pod "$TEST_POD" -n "$NAMESPACE"
    exit 1
fi

# Warten bis Counter hochzählt
info "Warte $WAIT_SECONDS Sekunden für Counter..."
sleep "$WAIT_SECONDS"

# Counter-Stand vor Checkpoint
COUNTER_BEFORE=$(microk8s kubectl logs "$TEST_POD" -n "$NAMESPACE" --tail=1 | grep -oP 'Counter: \K[0-9]+' || echo "0")
pass "Counter vor Checkpoint: $COUNTER_BEFORE"

# ============================================
# Checkpoint erstellen
# ============================================
subheader "Checkpoint erstellen"

# Container-ID ermitteln (suche nach python image)
CONTAINER_ID=$(get_container_id "python")

if [[ -z "$CONTAINER_ID" ]]; then
    fail "Container-ID konnte nicht ermittelt werden"
    info "Verfügbare Container:"
    sudo microk8s ctr --namespace k8s.io container ls | grep -v pause | head -10
    cleanup_pod "$TEST_POD" "$NAMESPACE"
    exit 1
fi
pass "Container-ID: ${CONTAINER_ID:0:12}..."

# Checkpoint ausführen
sudo mkdir -p /tmp/checkpoint-work
if sudo microk8s ctr --namespace k8s.io task checkpoint "$CONTAINER_ID" \
    --checkpoint-path /tmp/checkpoint \
    --work-path /tmp/checkpoint-work &>/dev/null; then
    pass "Checkpoint-Befehl erfolgreich"
else
    fail "Checkpoint-Befehl fehlgeschlagen"
    exit 1
fi

# Prüfen ob Shim den Checkpoint kopiert hat
sleep 2
if sudo test -d "$CHECKPOINT_PATH" && sudo test -f "$CHECKPOINT_PATH/pstree.img"; then
    CHECKPOINT_SIZE=$(sudo du -sh "$CHECKPOINT_PATH" | awk '{print $1}')
    pass "Checkpoint erstellt: $CHECKPOINT_PATH ($CHECKPOINT_SIZE)"
else
    fail "Checkpoint nicht gefunden unter $CHECKPOINT_PATH"
    ls -la /tmp/ | grep checkpoint || true
    cleanup_pod "$TEST_POD" "$NAMESPACE"
    exit 1
fi

# Shim-Log prüfen
if sudo grep -q "Checkpointing container" /tmp/kybernate-shim.log 2>/dev/null; then
    pass "Shim-Log zeigt Checkpoint"
else
    warn "Checkpoint nicht im Shim-Log gefunden"
fi

# ============================================
# Original-Pod löschen
# ============================================
subheader "Original-Pod löschen"

microk8s kubectl delete pod "$TEST_POD" -n "$NAMESPACE" --force --grace-period=0 &>/dev/null
sleep 2
pass "Original-Pod gelöscht"

# ============================================
# Restore-Pod starten
# ============================================
subheader "Restore-Pod starten"

cat <<EOF | microk8s kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $RESTORE_POD
  namespace: $NAMESPACE
spec:
  runtimeClassName: kybernate
  containers:
  - name: counter
    image: python:3.9-slim
    command: ["sleep", "infinity"]
    env:
    - name: RESTORE_FROM
      value: "$CHECKPOINT_PATH"
EOF

if wait_for_pod "$RESTORE_POD" "$NAMESPACE" 60; then
    pass "Restore-Pod gestartet"
else
    fail "Restore-Pod konnte nicht gestartet werden"
    microk8s kubectl describe pod "$RESTORE_POD" -n "$NAMESPACE"
    exit 1
fi

# Warten auf Restore-Stabilisierung
sleep 3

# ============================================
# Restore verifizieren
# ============================================
subheader "Restore verifizieren"

# Shim-Log prüfen
if sudo grep -q "Restoring container from checkpoint" /tmp/kybernate-shim.log 2>/dev/null; then
    pass "Shim-Log zeigt Restore"
else
    warn "Restore nicht im Shim-Log gefunden"
fi

# Counter-Stand nach Restore
COUNTER_AFTER=$(microk8s kubectl logs "$RESTORE_POD" -n "$NAMESPACE" --tail=1 2>/dev/null | grep -oP 'Counter: \K[0-9]+' || echo "0")

if [[ "$COUNTER_AFTER" -ge "$COUNTER_BEFORE" ]]; then
    pass "Counter fortgesetzt: $COUNTER_BEFORE → $COUNTER_AFTER"
else
    if [[ "$COUNTER_AFTER" -eq 0 ]]; then
        fail "Counter bei 0 gestartet (Restore fehlgeschlagen)"
        info "Logs:"
        microk8s kubectl logs "$RESTORE_POD" -n "$NAMESPACE" --tail=5 || true
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
summary "CPU Checkpoint/Restore (Task 05)"
