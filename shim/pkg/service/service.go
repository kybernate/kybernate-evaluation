package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	task "github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/runtime/v2/shim"
	runc "github.com/containerd/containerd/runtime/v2/runc/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Service wraps the runc shim to add checkpoint/restore capabilities.
type Service struct {
	shim.Shim
}

// New initializes the shim by delegating to the default runc shim.
func New(ctx context.Context, id string, publisher shim.Publisher, shutdown func()) (shim.Shim, error) {
	runcShim, err := runc.New(ctx, id, publisher, shutdown)
	if err != nil {
		return nil, err
	}
	return &Service{Shim: runcShim}, nil
}

// Create intercepts the container creation to check for restore annotations.
func (s *Service) Create(ctx context.Context, req *task.CreateTaskRequest) (*task.CreateTaskResponse, error) {
	debugLog(fmt.Sprintf("Create called. Bundle: %s", req.Bundle))

	// Check for restore annotation in the OCI spec
	if req.Bundle != "" {
		configPath := filepath.Join(req.Bundle, "config.json")
		data, err := os.ReadFile(configPath)
		if err == nil {
			var spec specs.Spec
			if err := json.Unmarshal(data, &spec); err == nil {
				// Check for restore annotation
				if checkpointPath, ok := spec.Annotations["kybernate.io/restore-from"]; ok {
					req.Checkpoint = checkpointPath
					debugLog(fmt.Sprintf("Restoring container from checkpoint (Annotation): %s", checkpointPath))
				}

				// Check for restore ENV var
				if spec.Process != nil {
					for _, env := range spec.Process.Env {
						if strings.HasPrefix(env, "RESTORE_FROM=") {
							checkpointPath := strings.TrimPrefix(env, "RESTORE_FROM=")
							req.Checkpoint = checkpointPath
							debugLog(fmt.Sprintf("Restoring container from checkpoint (ENV): %s", checkpointPath))
							break
						}
					}
				}
			}
		}
	}
	return s.Shim.Create(ctx, req)
}

// Checkpoint intercepts the checkpoint request.
func (s *Service) Checkpoint(ctx context.Context, req *task.CheckpointTaskRequest) (*emptypb.Empty, error) {
	debugLog(fmt.Sprintf("Checkpointing container to: %s", req.Path))
	resp, err := s.Shim.Checkpoint(ctx, req)
	if err == nil {
		// Copy checkpoint to /tmp/kybernate-checkpoint for testing
		// We use a fixed path, overwriting previous
		exec.Command("rm", "-rf", "/tmp/kybernate-checkpoint").Run()
		// req.Path is the directory where runc wrote the checkpoint
		cmd := exec.Command("cp", "-r", req.Path, "/tmp/kybernate-checkpoint")
		if err := cmd.Run(); err != nil {
			debugLog(fmt.Sprintf("Failed to copy checkpoint: %v", err))
		} else {
			debugLog("Copied checkpoint to /tmp/kybernate-checkpoint")
		}
	}
	return resp, err
}

func debugLog(msg string) {
	f, err := os.OpenFile("/tmp/kybernate-shim.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(msg + "\n")
}
