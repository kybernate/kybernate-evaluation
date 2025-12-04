package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	task "github.com/containerd/containerd/api/runtime/task/v2"
	// Import runc options to register the protobuf type
	_ "github.com/containerd/containerd/api/types/runc/options"
	runc "github.com/containerd/containerd/runtime/v2/runc/v2"
	"github.com/containerd/containerd/runtime/v2/shim"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/kybernate/kybernate/pkg/cuda"
)

// Service wraps the runc shim to add checkpoint/restore capabilities.
//
// GPU Checkpoint/Restore:
// - Uses CUDA Checkpoint API for VRAM ↔ Host RAM transfer
// - Uses CRIU (via runc) for Host RAM ↔ Disk transfer
// - Two-stage process: CUDA checkpoint before CRIU, CUDA restore after CRIU
type Service struct {
	shim.Shim
	cudaCheckpointer *cuda.Checkpointer
	gpuAvailable     bool
}

// New initializes the shim by delegating to the default runc shim.
func New(ctx context.Context, id string, publisher shim.Publisher, shutdown func()) (shim.Shim, error) {
	debugLog("Kybernate shim starting")

	runcShim, err := runc.New(ctx, id, publisher, shutdown)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		Shim:         runcShim,
		gpuAvailable: cuda.HasGPU(),
	}

	// Initialize CUDA checkpointer if GPU is available
	if svc.gpuAvailable {
		checkpointer, err := cuda.NewCheckpointer()
		if err != nil {
			debugLog(fmt.Sprintf("CUDA checkpointer init failed (GPU checkpoint disabled): %v", err))
		} else {
			svc.cudaCheckpointer = checkpointer
			debugLog("CUDA checkpointer initialized - GPU checkpoint enabled")
		}
	} else {
		debugLog("No GPU detected - GPU checkpoint disabled")
	}

	return svc, nil
}

// Options represents runtime options for the container
type Options struct {
	BinaryName    string `json:"binary_name,omitempty"`
	Root          string `json:"root,omitempty"`
	SystemdCgroup bool   `json:"systemd_cgroup,omitempty"`
}

// hasGPUResources checks if the OCI spec requests GPU resources
func hasGPUResources(spec *specs.Spec) bool {
	// Check for nvidia.com/gpu in Linux resources
	if spec.Linux != nil && spec.Linux.Resources != nil {
		for _, device := range spec.Linux.Resources.Devices {
			if device.Allow && device.Major != nil && *device.Major == 195 {
				// 195 is the major number for nvidia devices
				return true
			}
		}
	}

	// Check annotations for GPU requests
	if spec.Annotations != nil {
		if _, ok := spec.Annotations["io.kubernetes.cri.nvidia-gpu-quantity"]; ok {
			return true
		}
	}

	// Check process environment for NVIDIA-related vars
	if spec.Process != nil {
		for _, env := range spec.Process.Env {
			if strings.HasPrefix(env, "NVIDIA_VISIBLE_DEVICES=") ||
				strings.HasPrefix(env, "NVIDIA_DRIVER_CAPABILITIES=") {
				return true
			}
		}
	}

	return false
}

// ensureNvidiaRuntime writes options.json to use nvidia-container-runtime if GPU is requested
func ensureNvidiaRuntime(bundlePath string, spec *specs.Spec) error {
	if !hasGPUResources(spec) {
		return nil
	}

	// Check if nvidia-container-runtime is available
	if _, err := exec.LookPath("nvidia-container-runtime"); err != nil {
		debugLog("nvidia-container-runtime not found, using default runtime")
		return nil
	}

	optionsPath := filepath.Join(bundlePath, "options.json")

	// Check if options.json already exists
	if data, err := os.ReadFile(optionsPath); err == nil {
		var opts Options
		if err := json.Unmarshal(data, &opts); err == nil {
			if opts.BinaryName != "" {
				debugLog(fmt.Sprintf("options.json already has BinaryName: %s", opts.BinaryName))
				return nil
			}
		}
	}

	// Write options.json with nvidia-container-runtime
	opts := Options{
		BinaryName: "nvidia-container-runtime",
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return err
	}

	if err := os.WriteFile(optionsPath, data, 0644); err != nil {
		return err
	}

	debugLog("Wrote options.json with nvidia-container-runtime for GPU workload")
	return nil
}

// Create intercepts the container creation to check for restore annotations.
func (s *Service) Create(ctx context.Context, req *task.CreateTaskRequest) (*task.CreateTaskResponse, error) {
	debugLog(fmt.Sprintf("Create called. Bundle: %s", req.Bundle))

	isRestore := false
	checkpointPath := ""
	var spec *specs.Spec

	// Check for restore annotation in the OCI spec
	if req.Bundle != "" {
		configPath := filepath.Join(req.Bundle, "config.json")
		data, err := os.ReadFile(configPath)
		if err == nil {
			spec = &specs.Spec{}
			if err := json.Unmarshal(data, spec); err == nil {
				// Check for restore annotation
				if cp, ok := spec.Annotations["kybernate.io/restore-from"]; ok {
					req.Checkpoint = cp
					checkpointPath = cp
					isRestore = true
					debugLog(fmt.Sprintf("Restoring container from checkpoint (Annotation): %s", cp))
				}

				// Check for restore ENV var
				if spec.Process != nil {
					for _, env := range spec.Process.Env {
						if strings.HasPrefix(env, "RESTORE_FROM=") {
							cp := strings.TrimPrefix(env, "RESTORE_FROM=")
							req.Checkpoint = cp
							checkpointPath = cp
							isRestore = true
							debugLog(fmt.Sprintf("Restoring container from checkpoint (ENV): %s", cp))
							break
						}
					}
				}

				// Ensure nvidia-container-runtime is used for GPU workloads
				if err := ensureNvidiaRuntime(req.Bundle, spec); err != nil {
					debugLog(fmt.Sprintf("Failed to set nvidia-container-runtime: %v", err))
				}
			}
		}
	}

	// Call the underlying shim to create/restore the container
	resp, err := s.Shim.Create(ctx, req)
	if err != nil {
		return resp, err
	}

	// If this was a restore and we have GPU support, perform CUDA restore
	if isRestore && s.cudaCheckpointer != nil {
		debugLog(fmt.Sprintf("Checking for GPU process to restore (checkpoint: %s)", checkpointPath))

		// Wait a moment for the process to start
		time.Sleep(500 * time.Millisecond)

		// Find GPU process in the restored container
		if gpuPID, hasGPU := cuda.FindAnyGPUProcessForTask(int(resp.Pid)); hasGPU {
			debugLog(fmt.Sprintf("Found GPU process %d, performing CUDA restore", gpuPID))

			// Check if process is in checkpointed state
			state, err := s.cudaCheckpointer.GetState(gpuPID)
			if err != nil {
				debugLog(fmt.Sprintf("Failed to get CUDA state for PID %d: %v", gpuPID, err))
			} else if state == cuda.StateCheckpointed {
				// Perform CUDA restore: Host RAM → VRAM
				if err := s.cudaCheckpointer.RestoreFull(gpuPID); err != nil {
					debugLog(fmt.Sprintf("CUDA restore failed for PID %d: %v", gpuPID, err))
				} else {
					debugLog(fmt.Sprintf("CUDA restore successful for PID %d - VRAM restored", gpuPID))
				}
			} else {
				debugLog(fmt.Sprintf("GPU process %d not in checkpointed state (state=%s), skipping CUDA restore", gpuPID, state))
			}
		} else {
			debugLog("No GPU process found in restored container")
		}
	}

	return resp, nil
}

// Checkpoint intercepts the checkpoint request.
func (s *Service) Checkpoint(ctx context.Context, req *task.CheckpointTaskRequest) (*emptypb.Empty, error) {
	debugLog(fmt.Sprintf("Checkpointing container %s to: %s", req.ID, req.Path))

	// If GPU support is available, perform CUDA checkpoint first
	if s.cudaCheckpointer != nil {
		// Get the task PID to find GPU processes
		taskPID := s.getTaskPID(req.ID)
		if taskPID > 0 {
			if gpuPID, hasGPU := cuda.FindAnyGPUProcessForTask(taskPID); hasGPU {
				debugLog(fmt.Sprintf("Found GPU process %d, performing CUDA checkpoint (VRAM → RAM)", gpuPID))

				// Check current state
				state, err := s.cudaCheckpointer.GetState(gpuPID)
				if err != nil {
					debugLog(fmt.Sprintf("Failed to get CUDA state for PID %d: %v", gpuPID, err))
				} else if state == cuda.StateRunning {
					// Perform CUDA checkpoint: VRAM → Host RAM
					// Use 30 second timeout for large GPU workloads
					if err := s.cudaCheckpointer.CheckpointFull(gpuPID, 30000); err != nil {
						debugLog(fmt.Sprintf("CUDA checkpoint failed for PID %d: %v", gpuPID, err))
						// Continue with CRIU checkpoint anyway - GPU state might be lost
					} else {
						debugLog(fmt.Sprintf("CUDA checkpoint successful for PID %d - VRAM freed", gpuPID))
					}
				} else {
					debugLog(fmt.Sprintf("GPU process %d not in running state (state=%s), skipping CUDA checkpoint", gpuPID, state))
				}
			} else {
				debugLog("No GPU process found in container - CPU-only checkpoint")
			}
		}
	}

	// Now perform the CRIU checkpoint via runc
	resp, err := s.Shim.Checkpoint(ctx, req)
	if err == nil {
		// Copy checkpoint to /tmp/kybernate-checkpoint for testing
		exec.Command("rm", "-rf", "/tmp/kybernate-checkpoint").Run()
		cmd := exec.Command("cp", "-r", req.Path, "/tmp/kybernate-checkpoint")
		if err := cmd.Run(); err != nil {
			debugLog(fmt.Sprintf("Failed to copy checkpoint: %v", err))
		} else {
			debugLog("Copied checkpoint to /tmp/kybernate-checkpoint")
		}
	}
	return resp, err
}

// getTaskPID returns the PID of the container's init process
func (s *Service) getTaskPID(containerID string) int {
	// Read PID from containerd's task state
	// This is a simplified approach - in production, use containerd API
	pidFiles := []string{
		filepath.Join("/run/containerd/io.containerd.runtime.v2.task/k8s.io", containerID, "init.pid"),
		filepath.Join("/var/snap/microk8s/common/run/containerd/io.containerd.runtime.v2.task/k8s.io", containerID, "init.pid"),
	}

	for _, pidFile := range pidFiles {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				return pid
			}
		}
	}

	debugLog(fmt.Sprintf("Could not find init.pid for container %s", containerID))
	return 0
}

func debugLog(msg string) {
	f, err := os.OpenFile("/tmp/kybernate-shim.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(msg + "\n")
}
