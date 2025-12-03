#!/usr/bin/env bash
# Kybernate Test Utilities
# Gemeinsame Funktionen für alle Validierungs-Scripts

# Farben
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Zähler
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_WARNED=0

# Ausgabe-Funktionen
pass() {
    echo -e "  ${GREEN}✓${NC} $1"
    ((TESTS_PASSED++))
}

fail() {
    echo -e "  ${RED}✗${NC} $1"
    ((TESTS_FAILED++))
}

warn() {
    echo -e "  ${YELLOW}⚠${NC} $1"
    ((TESTS_WARNED++))
}

info() {
    echo -e "  ${BLUE}ℹ${NC} $1"
}

header() {
    echo -e "\n${BLUE}=== $1 ===${NC}"
}

subheader() {
    echo -e "\n${BLUE}--- $1 ---${NC}"
}

# Zusammenfassung ausgeben
summary() {
    local test_name="${1:-Tests}"
    echo ""
    header "Zusammenfassung: $test_name"
    echo -e "  Passed:  ${GREEN}$TESTS_PASSED${NC}"
    echo -e "  Failed:  ${RED}$TESTS_FAILED${NC}"
    echo -e "  Warned:  ${YELLOW}$TESTS_WARNED${NC}"
    
    if [[ $TESTS_FAILED -eq 0 ]]; then
        echo -e "\n${GREEN}Alle kritischen Tests bestanden!${NC}"
        return 0
    else
        echo -e "\n${RED}Einige Tests sind fehlgeschlagen!${NC}"
        return 1
    fi
}

# Prüft ob ein Befehl existiert
command_exists() {
    command -v "$1" &>/dev/null
}

# Prüft ob ein Befehl erfolgreich ist
command_succeeds() {
    "$@" &>/dev/null
}

# Wartet auf Pod-Ready-Status
wait_for_pod() {
    local pod_name="$1"
    local namespace="${2:-default}"
    local timeout="${3:-60}"
    
    microk8s kubectl wait --for=condition=Ready "pod/$pod_name" -n "$namespace" --timeout="${timeout}s" &>/dev/null
}

# Holt Container-ID für einen Pod (sucht nach Image oder Container-Name)
get_container_id() {
    local search_term="$1"
    local namespace="${2:-default}"
    
    # Suche nach dem Suchbegriff in der Container-Liste (Image-Name oder Container-Name)
    sudo microk8s ctr --namespace k8s.io container ls 2>/dev/null | \
        grep -v "pause" | \
        grep -i "$search_term" | \
        grep -v POD | \
        head -1 | \
        awk '{print $1}'
}

# Cleanup-Funktion für Pods
cleanup_pod() {
    local pod_name="$1"
    local namespace="${2:-default}"
    
    microk8s kubectl delete pod "$pod_name" -n "$namespace" --force --grace-period=0 &>/dev/null || true
}

# Root-Check
require_root() {
    if [[ $EUID -ne 0 ]]; then
        echo -e "${RED}Fehler: Dieses Script muss als root ausgeführt werden.${NC}"
        echo "Bitte mit 'sudo' ausführen."
        exit 1
    fi
}

# Optional Root (für manche Checks)
is_root() {
    [[ $EUID -eq 0 ]]
}

# Script-Verzeichnis ermitteln
get_script_dir() {
    cd "$(dirname "${BASH_SOURCE[1]}")" && pwd
}

# Projekt-Root ermitteln
get_project_root() {
    local script_dir
    script_dir=$(get_script_dir)
    cd "$script_dir" && git rev-parse --show-toplevel 2>/dev/null || echo "$script_dir/../.."
}
