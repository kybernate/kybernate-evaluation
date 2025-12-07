# Failure Modes & Recovery

Dieses Dokument beschreibt Strategien zur Fehlerbehandlung und Wiederherstellung ("Unhappy Paths") im Kybernate-System. Da Checkpoint/Restore komplexe Zustandsübergänge beinhaltet, ist eine robuste Fehlerbehandlung essenziell.

## Szenario A: Node Agent Absturz während Checkpoint

**Situation:** Der Node Agent stürzt ab oder wird beendet, während er gerade den VRAM dumpt oder CRIU ausführt.

1.  **Erkennung:**
    *   Der `containerd-shim` verliert die Verbindung zum Agent-Socket (EOF/Timeout).
    *   Der gRPC-Call `Checkpoint()` kehrt mit Fehler zurück.

2.  **Reaktion (Shim):**
    *   Der Shim muss den Container sofort killen (`SIGKILL`).
    *   **Grund:** Der Prozess befindet sich in einem undefinierten Zustand (CUDA Context evtl. gelockt, Filesystem gefroren, aber Checkpoint nicht abgeschlossen). Ein "Resume" ist oft nicht sicher möglich.

3.  **Reaktion (Operator):**
    *   Sieht den Pod als `Failed` oder `Unknown`.
    *   Prüft Metadata-Registry: Checkpoint ist unvollständig (kein `metadata.json` oder Checksum-Mismatch).

4.  **Recovery:**
    *   Operator markiert den Checkpoint-Versuch als `Failed`.
    *   Startet den Pod neu (Standard K8s CrashLoopBackOff greift).
    *   Falls es ein geplanter Checkpoint (Preemption) war, wird der Job neu eingeplant (Re-Queue).

## Szenario B: Restore schlägt fehl (z.B. VRAM OOM)

**Situation:** Der Restore-Prozess startet, aber die GPU hat nicht genug freien Speicher für den VRAM-Dump, oder CRIU scheitert an PID-Konflikten.

1.  **Erkennung:**
    *   Agent meldet Fehler im `RestoreResponse` (z.B. "Out of Memory", "CRIU restore failed").

2.  **Reaktion (Shim):**
    *   Bricht den Container-Start ab (`Create` call fails).
    *   Containerd meldet `StartError`.

3.  **Reaktion (Operator):**
    *   Empfängt Pod-Event `Failed`.

4.  **Recovery:**
    *   **VRAM-Mangel:** Operator kann versuchen, den Pod auf einen anderen Node mit mehr freiem VRAM zu schedulen (Update des `RestoreRequest` mit neuem Ziel).
    *   **CRIU-Fehler:** Wenn der Fehler deterministisch ist (z.B. Kernel-Inkompatibilität), wird der Restore abgebrochen und ein Alert gefeuert.

## Szenario C: Storage Tier nicht verfügbar (S3 Down)

**Situation:** Ein Checkpoint soll nach S3 ausgelagert werden, aber der Upload schlägt fehl.

1.  **Reaktion (Sidecar):**
    *   Versucht Retries (Exponential Backoff).
    *   Falls endgültig fehlgeschlagen: Behält Checkpoint im lokalen Cache (NVMe) und markiert ihn in der Registry als `tier: nvme` (dirty/pending upload).

2.  **Recovery:**
    *   Ein Hintergrund-Job im Sidecar oder Storage Tier Manager versucht später erneut, die Artefakte nach S3 zu promoten.
    *   Der Checkpoint bleibt lokal nutzbar, solange der Node lebt.

## State Machine & Konsistenz

Um Inkonsistenzen zu vermeiden, gilt:
*   **Metadata Commit:** Ein Checkpoint gilt erst als valide, wenn `metadata.json` erfolgreich geschrieben und validiert wurde.
*   **Atomic Switch:** Beim Restore wird erst auf `Running` geschaltet, wenn sowohl CRIU als auch CUDA erfolgreich waren.
