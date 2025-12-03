# Task 05: Shim Checkpoint & Restore Logic

**Status**: Pending
**Phase**: 1 (Foundation)

## Ziel
Erweiterung des `shim-kybernate-v1` Skeletons um echte Checkpoint- und Restore-Funktionalität. Der Shim soll in der Lage sein, auf Anfrage von Containerd (via CRI/Kubelet) einen Container zu checkpointen und wiederherzustellen.

## Kontext
In Task 04 haben wir das Shim-Gerüst gebaut, das Calls einfach an `runc` weiterleitet. In Task 03 haben wir bewiesen, dass `criu dump --leave-running` funktioniert. Jetzt müssen wir diese Logik in den Shim integrieren.

## Schritte

1.  **Shim Refactoring**:
    *   Erstelle eine neue Struktur `Service` in `pkg/service/service.go`, die `shim.Shim` implementiert (und das Ergebnis von `runc.New` einbettet).
    *   Dies ermöglicht uns, Methoden wie `Create` und `Checkpoint` zu überschreiben (Intercept).

2.  **Checkpoint Implementierung**:
    *   Implementiere `Checkpoint` Methode.
    *   Vorerst: Logging des Calls und Weiterleitung an `runc` Shim.
    *   Ziel: Sicherstellen, dass wir den Pfad kontrollieren können (optional).

3.  **Restore Implementierung (The Magic)**:
    *   Implementiere `Create` Methode.
    *   Lese die `config.json` aus dem `req.Bundle` Verzeichnis.
    *   Suche nach einer Annotation (z.B. `kybernate.io/restore-from`).
    *   Wenn die Annotation existiert:
        *   Setze `req.Checkpoint` auf den Pfad aus der Annotation.
        *   Setze `req.Options` ggf. so, dass `runc` weiß, dass es ein Restore ist (passiert meist automatisch wenn `Checkpoint` gesetzt ist).
    *   Rufe `s.Shim.Create` auf.

4.  **Test Workflow**:
    *   **Deploy**: Pod A starten (ganz normal).
    *   **Checkpoint**: Via `ctr` auf dem Node den Checkpoint erstellen (da K8s API das noch nicht kann).
        ```bash
        ctr -n k8s.io tasks checkpoint --image-path /tmp/test-checkpoint <container-id>
        ```
    *   **Restore**: Pod B starten, mit Annotation `kybernate.io/restore-from: /tmp/test-checkpoint`.
    *   **Verify**: Prüfen, ob Pod B den State von Pod A hat.

## Technische Herausforderungen
*   **Containerd API**: Wie genau wird der Checkpoint-Request von Containerd an den Shim v2 übergeben? -> `CheckpointTaskRequest` hat `Path`.
*   **Image Handling**: Wie kommen die Checkpoint-Daten vom Node weg (für Migration) oder bleiben sie lokal (für Scale-to-Zero)? -> Vorerst lokal.
*   **OCI Spec Parsing**: Wir müssen `config.json` parsen, um die Annotationen zu lesen.

## Definition of Done
*   [x] Shim Code refactored (Wrapper Struct).
*   [x] `Create` Methode wertet Annotation und ENV (`RESTORE_FROM`) aus.
*   [x] Manueller Test (ctr checkpoint -> Pod restore) erfolgreich.

## Learnings
*   **Annotations**: Kubernetes Pod Annotations werden von MicroK8s/Containerd NICHT automatisch in die OCI Spec des Workload-Containers übernommen (nur Sandbox).
*   **Workaround**: Wir nutzen Environment Variables (`RESTORE_FROM`), da diese zuverlässig in `config.json` landen.
*   **Checkpoint Path**: `ctr tasks checkpoint` schreibt in temporäre Pfade. Für den Test haben wir im Shim einen Hack eingebaut, der den Checkpoint nach `/tmp/kybernate-checkpoint` kopiert.
