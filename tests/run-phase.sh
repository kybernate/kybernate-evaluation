#!/usr/bin/env bash
# Führt alle Tests einer Phase aus
# Usage: ./run-phase.sh <phase-nummer> [test-name]

set -uo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# Farben
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

usage() {
    echo "Usage: $0 <phase> [test]"
    echo ""
    echo "Beispiele:"
    echo "  $0 1                    # Alle Phase 1 Tests"
    echo "  $0 1 host-dependencies  # Nur Host-Dependencies Test"
    echo "  $0 1 task05             # Nur Task 05 Test"
    echo ""
    echo "Verfügbare Phasen:"
    for phase_dir in "$SCRIPT_DIR"/phase*/; do
        if [[ -d "$phase_dir" ]]; then
            phase_name=$(basename "$phase_dir")
            echo "  ${phase_name#phase}"
        fi
    done
    exit 1
}

[[ $# -lt 1 ]] && usage

PHASE="$1"
TEST_FILTER="${2:-}"
PHASE_DIR="$SCRIPT_DIR/phase$PHASE"

if [[ ! -d "$PHASE_DIR" ]]; then
    echo -e "${RED}Fehler: Phase $PHASE nicht gefunden${NC}"
    echo "Verzeichnis existiert nicht: $PHASE_DIR"
    exit 1
fi

echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║              Kybernate Phase $PHASE Tests                       ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

for test_script in "$PHASE_DIR"/*.sh; do
    [[ ! -f "$test_script" ]] && continue
    
    test_name=$(basename "$test_script" .sh)
    
    # Filter anwenden
    if [[ -n "$TEST_FILTER" ]] && [[ "$test_name" != *"$TEST_FILTER"* ]]; then
        continue
    fi
    
    echo -e "${BLUE}▶ Running: $test_name${NC}"
    echo "─────────────────────────────────────────────────────────────"
    
    ((TOTAL_TESTS++))
    
    if bash "$test_script"; then
        ((PASSED_TESTS++))
        echo -e "${GREEN}✓ $test_name PASSED${NC}"
    else
        ((FAILED_TESTS++))
        echo -e "${RED}✗ $test_name FAILED${NC}"
    fi
    
    echo ""
done

echo "═════════════════════════════════════════════════════════════"
echo -e "Phase $PHASE Gesamt: ${GREEN}$PASSED_TESTS passed${NC}, ${RED}$FAILED_TESTS failed${NC} von $TOTAL_TESTS Tests"
echo "═════════════════════════════════════════════════════════════"

[[ $FAILED_TESTS -eq 0 ]]
