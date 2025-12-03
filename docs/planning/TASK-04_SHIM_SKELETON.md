# Task 04: Shim Skeleton & MicroK8s Integration

**Status**: Completed
**Phase**: 1 (Foundation)

## Ziel
Ein minimaler Containerd Shim (`shim-kybernate-v1`), der von MicroK8s aufgerufen werden kann, aber noch keine echte Checkpoint-Logik enthält (Pass-Through zu `runc`).

## Ergebnisse (Update 02.12.2025)
*   **Shim Skeleton:** Implementiert in Go. Nutzt `github.com/containerd/containerd/runtime/v2/runc/v2` als Basis und leitet alle Calls weiter.
*   **Integration:** MicroK8s `containerd-template.toml` angepasst, um `io.containerd.kybernate.v1` auf das Binary `/usr/local/bin/containerd-shim-kybernate-v1` zu mappen.
*   **Test:** Pod `shim-test` startet erfolgreich (`Running`) und der Shim-Prozess ist auf dem Host sichtbar.

## Schritte

1.  **Go Projekt Setup**:
    *   Initialisiere `go mod`.
    *   Importiere `github.com/containerd/containerd`.

2.  **Shim Implementierung (Skeleton)**:
    *   Implementiere das `shim.Shim` Interface (v2).
    *   Implementiere die grundlegenden Methoden (`Start`, `Stop`, `Shutdown`, etc.).
    *   Leite alle Calls vorerst an den Standard `runc` Shim weiter (oder nutze `containerd/go-runc` direkt).

3.  **Build & Install**:
    *   Baue das Binary `containerd-shim-kybernate-v1`.
    *   Installiere es in den Pfad, den MicroK8s sieht (siehe `05_MICROK8S_STRATEGY.md`).

4.  **MicroK8s Konfiguration**:
    *   Editiere das `containerd-template.toml` in MicroK8s.
    *   Registriere die Runtime `kybernate`.

5.  **Test**:
    *   Erstelle einen Pod mit `runtimeClassName: kybernate`.
    *   Prüfe, ob der Pod startet (`Running`).
    *   Prüfe in den Logs des Shims (stdout/stderr oder File), ob er aufgerufen wurde.

## Definition of Done
*   [x] Shim Binary kompiliert.
*   [x] MicroK8s ist konfiguriert.
*   [x] Ein "Hello World" Pod startet erfolgreich mit der neuen RuntimeClass.
