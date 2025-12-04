// Package checkpoint implements GPU-aware container checkpoint/restore for Kubernetes.
// It uses the Kubernetes Checkpoint API (KEP-2008) combined with CUDA Checkpoint API
// to properly handle GPU workloads.
package checkpoint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kybernate/kybernate/pkg/cuda"
)

// CheckpointController manages GPU container checkpoint/restore operations
type CheckpointController struct {
	cudaCheckpointer *cuda.Checkpointer
	checkpointDir    string
	nodeName         string
}

// NewCheckpointController creates a new checkpoint controller
func NewCheckpointController(checkpointDir string) (*CheckpointController, error) {
	ckpt, err := cuda.NewCheckpointer()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize CUDA checkpointer: %w", err)
	}

	nodeName, _ := os.Hostname()

	return &CheckpointController{
		cudaCheckpointer: ckpt,
		checkpointDir:    checkpointDir,
		nodeName:         nodeName,
	}, nil
}

// CheckpointRequest contains information for a checkpoint operation
type CheckpointRequest struct {
	Namespace     string
	PodName       string
	ContainerName string
	ContainerID   string
	GPUProcessPID int
}

// CheckpointResult contains the result of a checkpoint operation
type CheckpointResult struct {
	CheckpointPath string
	CUDAState      string
	Duration       time.Duration
	Error          error
}

// Checkpoint performs a full GPU container checkpoint
// This implements the Two-Stage approach:
// 1. CUDA Checkpoint: VRAM → RAM (via CUDA Checkpoint API)
// 2. CRIU Checkpoint: RAM → Disk (via Kubernetes Checkpoint API)
func (c *CheckpointController) Checkpoint(ctx context.Context, req *CheckpointRequest) *CheckpointResult {
	start := time.Now()
	result := &CheckpointResult{}

	// Create checkpoint directory
	checkpointPath := filepath.Join(c.checkpointDir, req.Namespace, req.PodName, req.ContainerName)
	if err := os.MkdirAll(checkpointPath, 0755); err != nil {
		result.Error = fmt.Errorf("failed to create checkpoint directory: %w", err)
		return result
	}
	result.CheckpointPath = checkpointPath

	// Stage 1: CUDA Checkpoint (if GPU process)
	if req.GPUProcessPID > 0 {
		state, err := c.cudaCheckpointer.GetState(req.GPUProcessPID)
		if err != nil {
			result.Error = fmt.Errorf("failed to get CUDA state: %w", err)
			return result
		}
		result.CUDAState = state.String()

		if state == cuda.StateRunning {
			// Perform CUDA checkpoint: Lock + Checkpoint (VRAM → RAM)
			if err := c.cudaCheckpointer.CheckpointFull(req.GPUProcessPID, 60000); err != nil {
				result.Error = fmt.Errorf("CUDA checkpoint failed: %w", err)
				return result
			}
			result.CUDAState = "checkpointed"
		}
	}

	// Stage 2: Kubernetes Checkpoint API (CRIU)
	if err := c.kubernetesCheckpoint(ctx, req, checkpointPath); err != nil {
		// Try to restore CUDA state on failure
		if req.GPUProcessPID > 0 {
			_ = c.cudaCheckpointer.RestoreFull(req.GPUProcessPID)
		}
		result.Error = fmt.Errorf("Kubernetes checkpoint failed: %w", err)
		return result
	}

	result.Duration = time.Since(start)
	return result
}

// kubernetesCheckpoint uses kubectl checkpoint or kubelet API
func (c *CheckpointController) kubernetesCheckpoint(ctx context.Context, req *CheckpointRequest, checkpointPath string) error {
	// Try using kubectl checkpoint (Kubernetes 1.25+)
	cmd := exec.CommandContext(ctx, "kubectl", "checkpoint",
		req.PodName,
		"-n", req.Namespace,
		"-c", req.ContainerName,
		"--export-path", checkpointPath,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// Fallback: Use kubelet checkpoint API directly
	kubeletEndpoint := fmt.Sprintf("https://localhost:10250/checkpoint/%s/%s/%s",
		req.Namespace, req.PodName, req.ContainerName)

	curlCmd := exec.CommandContext(ctx, "curl", "-k", "-X", "POST",
		"--cert", "/var/snap/microk8s/current/certs/kubelet.crt",
		"--key", "/var/snap/microk8s/current/certs/kubelet.key",
		kubeletEndpoint,
	)
	output, err = curlCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubelet checkpoint failed: %v: %s", err, output)
	}

	return nil
}

// RestoreRequest contains information for a restore operation
type RestoreRequest struct {
	Namespace      string
	PodName        string
	ContainerName  string
	CheckpointPath string
}

// RestoreResult contains the result of a restore operation
type RestoreResult struct {
	NewContainerID string
	NewGPUPID      int
	CUDAState      string
	Duration       time.Duration
	Error          error
}

// Restore performs a full GPU container restore
// This implements the reverse of the Two-Stage approach:
// 1. CRIU Restore: Disk → RAM (create new container from checkpoint)
// 2. CUDA Restore: RAM → VRAM (restore GPU memory)
func (c *CheckpointController) Restore(ctx context.Context, req *RestoreRequest) *RestoreResult {
	start := time.Now()
	result := &RestoreResult{}

	// Stage 1: Create container from checkpoint
	containerID, gpuPID, err := c.restoreFromCheckpoint(ctx, req)
	if err != nil {
		result.Error = fmt.Errorf("container restore failed: %w", err)
		return result
	}
	result.NewContainerID = containerID
	result.NewGPUPID = gpuPID

	// Stage 2: CUDA Restore (if GPU process)
	if gpuPID > 0 {
		state, err := c.cudaCheckpointer.GetState(gpuPID)
		if err != nil {
			result.Error = fmt.Errorf("failed to get CUDA state: %w", err)
			return result
		}

		if state == cuda.StateCheckpointed {
			// Perform CUDA restore: Restore + Unlock (RAM → VRAM)
			if err := c.cudaCheckpointer.RestoreFull(gpuPID); err != nil {
				result.Error = fmt.Errorf("CUDA restore failed: %w", err)
				return result
			}
			result.CUDAState = "running"
		} else {
			result.CUDAState = state.String()
		}
	}

	result.Duration = time.Since(start)
	return result
}

// restoreFromCheckpoint creates a new container from checkpoint image
func (c *CheckpointController) restoreFromCheckpoint(ctx context.Context, req *RestoreRequest) (string, int, error) {
	// Use crictl to restore container
	// This requires the container runtime to support restore

	// For now, we create a new pod with annotation to restore from checkpoint
	// The actual restore is handled by the runtime via the checkpoint annotation

	// TODO: Implement proper restore via CRI API
	// The flow would be:
	// 1. Create container with checkpoint image
	// 2. containerd loads the checkpoint
	// 3. kybernate-runtime intercepts restore and handles CUDA restore

	return "", 0, fmt.Errorf("restore not yet implemented - requires CRI restore API")
}

// FindGPUProcess finds the GPU process PID for a container
func (c *CheckpointController) FindGPUProcess(containerID string) (int, error) {
	// Get container init PID from runc state
	statePath := filepath.Join("/run/containerd/runc/k8s.io", containerID, "state.json")

	// Read state.json to get init PID
	// Then find GPU child process using nvidia-smi

	cmd := exec.Command("nvidia-smi", "--query-compute-apps=pid", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		return 0, nil // No GPU processes
	}

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid int
		fmt.Sscanf(line, "%d", &pid)

		// Check if this PID belongs to the container
		// by checking /proc/PID/cgroup
		cgroupPath := fmt.Sprintf("/proc/%d/cgroup", pid)
		data, err := os.ReadFile(cgroupPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), containerID) {
			return pid, nil
		}
	}

	return 0, nil
}
