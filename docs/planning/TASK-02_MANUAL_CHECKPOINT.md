# Task 02: Manual Checkpoint (Non-K8s)

**Status**: Completed
**Phase**: 1 (Foundation)

## Ziel
Verständnis der genauen Befehlsketten und Parameter, die nötig sind, um einen GPU-Prozess zu checkpointen und wiederherzustellen, *bevor* wir dies in Go-Code automatisieren.

## Schritte

1.  **Test-Szenarien**:
    *   Nutze die detaillierten Anleitungen im `test/` Verzeichnis.
    *   **Basic**: `test/01_TEST_CHECKPOINTING.md` (Einfacher Counter).
    *   **Advanced**: `test/03_HEAVY_PYTORCH_DUMP.md` (Echter PyTorch Workload mit VRAM-Belegung).

2.  **Analyse**:
    *   Die Befehlsketten für `criu dump` und `criu restore` sind in den Test-Dokumenten dokumentiert und validiert.
    *   Wichtige Flags: `--shell-job`, `--tcp-established`, `--ext-unix-sk`, `--enable-fs hugetlbfs`.

## Definition of Done
*   [x] Ein reproduzierbares Verfahren zum Checkpointen von GPU-Containern existiert.
*   [x] Die notwendigen CLI-Parameter für CRIU und cuda-checkpoint sind bekannt und dokumentiert.
