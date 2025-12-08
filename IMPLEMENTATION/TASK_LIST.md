# Implementation Task List (Granular)

Diese Liste zerlegt die Implementierung von Kybernate in atomare, testbare Schritte. Jeder Haken repräsentiert einen konkreten Code-Change oder Validierungsschritt.

## Phase 1: Foundation & Scaffolding
*Ziel: Eine kompilierbare Basisstruktur steht.*

- [ ] **1.1 Project Root**: Erstelle die Verzeichnisse `cmd/`, `pkg/`, `manifests/` gemäß `PROJECT_STRUCTURE.md`.
- [ ] **1.2 Go Module**: Führe `go mod init github.com/kybernate/kybernate` aus.
- [ ] **1.3 Makefile**: Erstelle ein `Makefile` mit Targets für `build`, `clean`, `test`.
- [ ] **1.4 Proto Definition**: Erstelle `pkg/api/agent/v1/agent.proto` mit den Messages aus `API_SPEC.md`.
- [ ] **1.5 Proto Generate**: Generiere den Go-Code (`protoc`) und committe ihn.
- [ ] **1.6 Agent Skeleton**: Erstelle `cmd/kybernate-agent/main.go` (Startet, gibt Log aus, beendet).
- [ ] **1.7 Shim Skeleton**: Erstelle `cmd/containerd-shim-kybernate-v1/main.go` (Implementiert minimales Shim-Interface).
- [ ] **1.8 Build Verification**: Führe `make build` aus und prüfe, ob Binaries in `bin/` liegen.

## Phase 2: Node Agent (GPU Low-Level via CGO)
*Ziel: Direkte Kommunikation mit dem NVIDIA Treiber ohne externe Binaries.*

- [ ] **2.1 CGO Setup**: Erstelle `pkg/checkpoint/cuda/driver.go` mit `#cgo LDFLAGS: -lcuda`.
- [ ] **2.2 CUDA Init**: Implementiere Wrapper-Funktion für `cuInit(0)`.
- [ ] **2.3 Device Get**: Implementiere Wrapper für `cuDeviceGet`.
- [ ] **2.4 Context Create**: Implementiere Wrapper für `cuCtxCreate`.
- [ ] **2.5 Context Destroy**: Implementiere Wrapper für `cuCtxDestroy`.
- [ ] **2.6 Checkpoint Save**: Implementiere Wrapper für `cuCheckpointSave` (Signatur beachten!).
- [ ] **2.7 Checkpoint Restore**: Implementiere Wrapper für `cuCheckpointRestore`.
- [ ] **2.8 Test Tool**: Erstelle `cmd/cuda-test/main.go`, das die Wrapper importiert.
- [ ] **2.9 Test Logic**: Das Test-Tool soll Speicher auf GPU 0 allozieren und `cuCheckpointSave` aufrufen.
- [ ] **2.10 Validation**: Führe das Test-Tool auf einem GPU-Node aus und prüfe, ob Checkpoint-Dateien erstellt wurden.

## Phase 3: Node Agent (Containerd Integration)
*Ziel: Konsistente CPU/Memory Checkpoints.*

- [ ] **3.1 Containerd Dep**: Füge `github.com/containerd/containerd` zu `go.mod` hinzu.
- [ ] **3.2 Client Connect**: Implementiere Funktion `ConnectToContainerd(socketPath)`.
- [ ] **3.3 gRPC Server**: Implementiere den gRPC Server Stub für `NodeAgentService`.
- [ ] **3.4 Checkpoint Handler**: Implementiere `Checkpoint(ctx, req)` Methode.
- [ ] **3.5 GPU Trigger**: Rufe im Handler zuerst den GPU-Checkpoint (aus Phase 2) auf.
- [ ] **3.6 Containerd Checkpoint**: Rufe `task.Checkpoint()` via Containerd-Client auf.
- [ ] **3.7 Metadata Struct**: Definiere Go-Structs für `metadata.json`.
- [ ] **3.8 Metadata Write**: Schreibe die `metadata.json` nach erfolgreichem Checkpoint.
- [ ] **3.9 Error Handling**: Implementiere Rollback (Löschen von Dateien), falls ein Schritt fehlschlägt.

## Phase 4: Shim - Basic Intercept & Config Patching
*Ziel: Der Shim muss in den Container-Start eingreifen und die Umgebung vorbereiten.*

- [ ] **4.1 Runtime Class**: Erstelle `manifests/runtime-class.yaml` für `kybernate-gpu`.
- [ ] **4.2 Shim Create**: Implementiere `Create` Methode im Shim.
- [ ] **4.3 Annotation Check**: Lese `kybernate.io/restore-from` aus der OCI Spec.
- [ ] **4.4 Passthrough**: Wenn Annotation fehlt, rufe Standard-Logik (runc create) auf.
- [ ] **4.5 Config Reader**: Implementiere `pkg/shim/config.go` zum Einlesen von `config.json`.
- [ ] **4.6 Device Discovery**: Implementiere Funktion, die `/dev/nvidia*` Devices auflistet (oder hardcoded für MVP).
- [ ] **4.7 Mount Injection**: Implementiere Funktion, die Mounts in die OCI Spec Struct einfügt.
- [ ] **4.8 Lib Injection**: Füge Mounts für `libcuda.so` etc. hinzu.
- [ ] **4.9 Config Writer**: Implementiere Funktion zum Speichern der gepatchten `config.json`.
- [ ] **4.10 Unit Test**: Teste Patching-Logik mit einer Beispiel-Config (ohne echte Runtime).

## Phase 5: Shim - Restore Execution
*Ziel: Wiederbelebung des Prozesses.*

- [ ] **5.1 Runc Bindings**: Importiere `github.com/opencontainers/runc/libcontainer` (oder nutze exec wrapper).
- [ ] **5.2 Restore Command**: Baue den `runc restore` Befehl zusammen.
- [ ] **5.3 CRIU Flags**: Setze Flags wie `--manage-cgroups-mode=ignore`, `--tcp-established`.
- [ ] **5.4 Execute Restore**: Führe den Restore-Befehl im Shim aus (statt `create`).
- [ ] **5.5 Process Check**: Prüfe, ob der Prozess existiert und im Status `Paused` ist.
- [ ] **5.6 NetNS Handling**: Verifiziere, dass der Prozess im korrekten Netzwerk-Namespace läuft.

## Phase 6: Full Integration (GPU Restore)
*Ziel: Wiederherstellung des VRAMs und Mapping auf neue Hardware.*

- [ ] **6.1 Agent Restore Handler**: Implementiere `Restore(ctx, req)` im Agent.
- [ ] **6.2 VRAM Load**: Lese VRAM-Dump-Dateien.
- [ ] **6.3 Target GPU**: Ermittle die zugewiesene GPU-ID für den Ziel-Container.
- [ ] **6.4 CUDA Restore**: Rufe `cuCheckpointRestore` mit der Ziel-GPU-ID auf.
- [ ] **6.5 Shim-Agent Client**: Implementiere gRPC Client im Shim.
- [ ] **6.6 Handshake**: Shim ruft `Agent.Restore()` *nach* `runc restore`.
- [ ] **6.7 Resume**: Shim ruft `task.Start()` (Resume) auf, wenn Agent OK meldet.
- [ ] **6.8 E2E Test**: Führe den kompletten `TEST_WORKFLOW.md` durch.

## Phase 7: Cleanup & Optimization
*Ziel: Produktionsreife.*

- [ ] **7.1 Artifact Cleanup**: Implementiere Lösch-Logik für temporäre Dumps.
- [ ] **7.2 Robustness**: Fange `SIGKILL` / Abstürze ab.
- [ ] **7.3 Logging**: Strukturiertes Logging (JSON) für Shim und Agent.
- [ ] **7.4 Zero-Copy**: (Optional) Implementiere `splice()` für Datentransfer.
