# Task 01: Environment Setup & Verification

**Status**: Completed
**Phase**: 1 (Foundation)

## Ziel
Eine vollständig funktionierende Entwicklungsumgebung auf Basis von MicroK8s, in der alle Low-Level-Tools (`criu`, `cuda-checkpoint`) installiert und verifiziert sind.

## Schritte

1.  **Base Installation**:
    *   Führe die Schritte aus `docs/setup/01_INSTALL_DRIVER.md` aus.
    *   Führe die Schritte aus `docs/setup/02_INSTALL_MICROK8S.md` aus.

2.  **Verifikation**:
    *   Führe die Tests aus `test/01_TEST_CHECKPOINTING.md` aus, um CRIU und `cuda-checkpoint` zu validieren.
    *   Führe die Tests aus `test/02_TEST_MICROK8S.md` aus, um die MicroK8s-Installation zu prüfen.

## Definition of Done
*   [x] `nvidia-smi` zeigt die GPU auf dem Host und im Container.
*   [x] `criu check` meldet "Looks good".
*   [x] Ein manueller Testlauf von `cuda-checkpoint` mit einer Dummy-Applikation war erfolgreich (Dump & Restore).
