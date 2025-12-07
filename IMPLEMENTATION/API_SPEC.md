# API Specification

## Node Agent gRPC Service

Die Kommunikation zwischen `containerd-shim`, `Operator` und `Node-Agent` erfolgt über gRPC.
Nachfolgend die Definition für `pkg/api/agent/v1/agent.proto`.

```protobuf
syntax = "proto3";

package kybernate.agent.v1;

option go_package = "github.com/kybernate/kybernate/pkg/api/agent/v1";

service NodeAgentService {
  // Initiiert den Checkpoint-Prozess (CUDA Lock -> VRAM Dump -> CRIU)
  rpc Checkpoint(CheckpointRequest) returns (CheckpointResponse);
  
  // Bereitet den Restore vor (Prefetch -> CUDA Warmup -> CRIU Restore Trigger)
  rpc Restore(RestoreRequest) returns (RestoreResponse);
  
  // Abfrage des aktuellen Status einer Operation (für Polling/Monitoring)
  rpc GetOperationStatus(OperationStatusRequest) returns (OperationStatusResponse);
}

message CheckpointRequest {
  string container_id = 1;
  string pod_uid = 2;
  string namespace = 3;
  
  // Ziel-Konfiguration
  string checkpoint_id = 4; // Vom Operator generierte ID
  string destination_path = 5; // Lokaler Pfad oder Sidecar-Socket
  
  // Timeouts und Policy
  int32 timeout_seconds = 6;
  bool compress_local = 7; // Soll der Agent komprimieren oder das Sidecar?
}

message CheckpointResponse {
  bool success = 1;
  string error_message = 2;
  repeated string artifact_paths = 3; // Pfade zu den erzeugten Dumps
  int64 total_size_bytes = 4;
}

message RestoreRequest {
  string container_id = 1;
  string checkpoint_id = 2;
  string artifact_source_path = 3; // Wo liegen die Daten lokal?
  bool warmup_vram = 4; // Soll VRAM vor dem Prozess-Start geladen werden?
}

message RestoreResponse {
  bool success = 1;
  string error_message = 2;
}

message OperationStatusRequest {
  string operation_id = 1;
}

message OperationStatusResponse {
  string status = 1; // PENDING, RUNNING, COMPLETED, FAILED
  string phase = 2;  // e.g. "CUDA_DUMP", "CRIU_DUMP", "UPLOAD"
  int32 progress_percent = 3;
  string error_message = 4;
}
```
