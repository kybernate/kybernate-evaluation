// Package main implements the kybernate-ctl command line tool
// for managing GPU container checkpoints in Kubernetes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kybernate/kybernate/pkg/cuda"
)

const (
	defaultCheckpointDir = "/var/lib/kybernate/checkpoints"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "checkpoint":
		checkpointCmd(os.Args[2:])
	case "restore":
		restoreCmd(os.Args[2:])
	case "list":
		listCmd(os.Args[2:])
	case "status":
		statusCmd(os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`kybernate-ctl - GPU Container Checkpoint Manager

Usage:
  kybernate-ctl checkpoint -n <namespace> -p <pod> -c <container>
  kybernate-ctl restore -n <namespace> -p <pod> -c <container> --from <checkpoint-path>
  kybernate-ctl list [-n <namespace>]
  kybernate-ctl status -n <namespace> -p <pod> -c <container>

Commands:
  checkpoint   Create a GPU-aware checkpoint of a container
  restore      Restore a container from a checkpoint
  list         List available checkpoints
  status       Show checkpoint status of a container

Examples:
  # Checkpoint a GPU container
  kybernate-ctl checkpoint -n kybernate-system -p gpu-test -c cuda

  # Restore from checkpoint
  kybernate-ctl restore -n kybernate-system -p gpu-test -c cuda --from /var/lib/kybernate/checkpoints/...

  # List all checkpoints
  kybernate-ctl list
`)
}

func checkpointCmd(args []string) {
	fs := flag.NewFlagSet("checkpoint", flag.ExitOnError)
	namespace := fs.String("n", "default", "Namespace")
	pod := fs.String("p", "", "Pod name")
	container := fs.String("c", "", "Container name")
	outputDir := fs.String("o", defaultCheckpointDir, "Output directory")
	fs.Parse(args)

	if *pod == "" || *container == "" {
		fmt.Println("Error: pod and container are required")
		os.Exit(1)
	}

	fmt.Printf("Creating checkpoint for %s/%s/%s\n", *namespace, *pod, *container)
	fmt.Println("=" + strings.Repeat("=", 50))

	// Step 1: Get container ID
	containerID, err := getContainerID(*namespace, *pod, *container)
	if err != nil {
		fmt.Printf("Error getting container ID: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Container ID: %s\n", containerID)

	// Step 2: Find GPU process
	gpuPID := findGPUProcess(containerID)
	if gpuPID > 0 {
		fmt.Printf("GPU Process PID: %d\n", gpuPID)
	} else {
		fmt.Println("No GPU process detected (CPU-only checkpoint)")
	}

	// Step 3: Create checkpoint directory
	timestamp := time.Now().Format("20060102-150405")
	checkpointPath := filepath.Join(*outputDir, *namespace, *pod, *container, timestamp)
	if err := os.MkdirAll(checkpointPath, 0755); err != nil {
		fmt.Printf("Error creating checkpoint directory: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Checkpoint path: %s\n", checkpointPath)
	fmt.Println()

	// Step 4: CUDA Checkpoint (if GPU)
	if gpuPID > 0 {
		fmt.Println("[Stage 1/2] CUDA Checkpoint (VRAM → RAM)...")
		if err := cudaCheckpoint(gpuPID); err != nil {
			fmt.Printf("CUDA checkpoint failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ CUDA checkpoint successful - VRAM transferred to RAM")
		fmt.Println()
	}

	// Step 5: CRIU Checkpoint (RAM → Disk)
	fmt.Println("[Stage 2/2] CRIU Checkpoint (RAM → Disk)...")
	if err := criuCheckpoint(containerID, checkpointPath); err != nil {
		fmt.Printf("CRIU checkpoint failed: %v\n", err)
		// Try to restore CUDA state
		if gpuPID > 0 {
			_ = cudaRestore(gpuPID)
		}
		os.Exit(1)
	}
	fmt.Println("✓ CRIU checkpoint successful - container state saved to disk")
	fmt.Println()

	// Step 6: Save metadata
	metadata := map[string]interface{}{
		"namespace":      *namespace,
		"pod":            *pod,
		"container":      *container,
		"containerID":    containerID,
		"gpuPID":         gpuPID,
		"timestamp":      timestamp,
		"checkpointPath": checkpointPath,
	}
	metadataPath := filepath.Join(checkpointPath, "kybernate-metadata.json")
	metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
	os.WriteFile(metadataPath, metadataJSON, 0644)

	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Printf("✓ Checkpoint complete: %s\n", checkpointPath)

	// Show disk usage
	cmd := exec.Command("du", "-sh", checkpointPath)
	output, _ := cmd.Output()
	fmt.Printf("  Size: %s", output)
}

func restoreCmd(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	namespace := fs.String("n", "default", "Namespace")
	pod := fs.String("p", "", "Pod name")
	container := fs.String("c", "", "Container name")
	from := fs.String("from", "", "Checkpoint path to restore from")
	fs.Parse(args)

	if *from == "" {
		fmt.Println("Error: --from is required")
		os.Exit(1)
	}

	fmt.Printf("Restoring from checkpoint: %s\n", *from)
	fmt.Println("=" + strings.Repeat("=", 50))

	// Load metadata
	metadataPath := filepath.Join(*from, "kybernate-metadata.json")
	metadataJSON, err := os.ReadFile(metadataPath)
	if err != nil {
		fmt.Printf("Error reading metadata: %v\n", err)
		os.Exit(1)
	}

	var metadata map[string]interface{}
	json.Unmarshal(metadataJSON, &metadata)
	fmt.Printf("Original pod: %s/%s/%s\n", metadata["namespace"], metadata["pod"], metadata["container"])

	containerID := metadata["containerID"].(string)
	gpuPID := int(metadata["gpuPID"].(float64))

	// Step 1: CRIU Restore (Disk → RAM)
	fmt.Println()
	fmt.Println("[Stage 1/2] CRIU Restore (Disk → RAM)...")

	newContainerID, newPID, err := criuRestore(*from, *namespace, *pod, *container)
	if err != nil {
		fmt.Printf("CRIU restore failed: %v\n", err)
		fmt.Println()
		fmt.Println("Note: Full restore requires Kubernetes CRI restore support.")
		fmt.Println("Alternative: Create a new pod and use 'kybernate-ctl cuda-restore <pid>'")
		os.Exit(1)
	}
	fmt.Printf("✓ Container restored: %s (PID: %d)\n", newContainerID, newPID)

	// Step 2: CUDA Restore (if GPU)
	if gpuPID > 0 && newPID > 0 {
		fmt.Println()
		fmt.Println("[Stage 2/2] CUDA Restore (RAM → VRAM)...")
		if err := cudaRestore(newPID); err != nil {
			fmt.Printf("CUDA restore failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ CUDA restore successful - VRAM restored")
	}

	fmt.Println()
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Printf("✓ Restore complete\n")
	_ = containerID // Used in future implementation
}

func listCmd(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	namespace := fs.String("n", "", "Filter by namespace")
	fs.Parse(args)

	fmt.Println("Available checkpoints:")
	fmt.Println("=" + strings.Repeat("=", 70))

	baseDir := defaultCheckpointDir
	if *namespace != "" {
		baseDir = filepath.Join(baseDir, *namespace)
	}

	filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		metadataPath := filepath.Join(path, "kybernate-metadata.json")
		if _, err := os.Stat(metadataPath); err == nil {
			metadataJSON, _ := os.ReadFile(metadataPath)
			var metadata map[string]interface{}
			json.Unmarshal(metadataJSON, &metadata)

			fmt.Printf("%-50s %s/%s/%s\n",
				path,
				metadata["namespace"],
				metadata["pod"],
				metadata["container"],
			)
		}
		return nil
	})
}

func statusCmd(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	namespace := fs.String("n", "default", "Namespace")
	pod := fs.String("p", "", "Pod name")
	container := fs.String("c", "", "Container name")
	fs.Parse(args)

	if *pod == "" || *container == "" {
		fmt.Println("Error: pod and container are required")
		os.Exit(1)
	}

	containerID, err := getContainerID(*namespace, *pod, *container)
	if err != nil {
		fmt.Printf("Container not found: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Container: %s/%s/%s\n", *namespace, *pod, *container)
	fmt.Printf("Container ID: %s\n", containerID)

	gpuPID := findGPUProcess(containerID)
	if gpuPID > 0 {
		fmt.Printf("GPU Process PID: %d\n", gpuPID)

		ckpt, err := cuda.NewCheckpointer()
		if err == nil {
			state, err := ckpt.GetState(gpuPID)
			if err == nil {
				fmt.Printf("CUDA State: %s\n", state)
			}
		}

		// Show GPU memory
		cmd := exec.Command("nvidia-smi", "--query-compute-apps=pid,used_memory",
			"--format=csv,noheader")
		output, _ := cmd.Output()
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, fmt.Sprintf("%d", gpuPID)) {
				parts := strings.Split(line, ",")
				if len(parts) >= 2 {
					fmt.Printf("GPU Memory: %s\n", strings.TrimSpace(parts[1]))
				}
			}
		}
	} else {
		fmt.Println("GPU Process: None (CPU-only container)")
	}
}

// Helper functions

func getContainerID(namespace, pod, container string) (string, error) {
	cmd := exec.Command("crictl", "ps", "-q",
		"--label", fmt.Sprintf("io.kubernetes.pod.namespace=%s", namespace),
		"--label", fmt.Sprintf("io.kubernetes.pod.name=%s", pod),
		"--label", fmt.Sprintf("io.kubernetes.container.name=%s", container),
	)
	output, err := cmd.Output()
	if err != nil {
		// Fallback to kubectl + ctr
		cmd = exec.Command("bash", "-c", fmt.Sprintf(
			`sudo microk8s.ctr --namespace k8s.io containers ls | grep -v pause | grep "$(kubectl get pod %s -n %s -o jsonpath='{.status.containerStatuses[?(@.name=="%s")].containerID}' | sed 's/containerd:\/\///')" | awk '{print $1}'`,
			pod, namespace, container,
		))
		output, err = cmd.Output()
		if err != nil {
			return "", err
		}
	}

	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return "", fmt.Errorf("container not found")
	}
	return containerID, nil
}

func findGPUProcess(containerID string) int {
	cmd := exec.Command("nvidia-smi", "--query-compute-apps=pid", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	// Also get the container's init PID from runc state
	statePath := fmt.Sprintf("/run/containerd/runc/k8s.io/%s/state.json", containerID)
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		return 0
	}

	var state struct {
		InitProcessPID int `json:"init_process_pid"`
	}
	json.Unmarshal(stateData, &state)
	initPID := state.InitProcessPID

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid int
		fmt.Sscanf(line, "%d", &pid)

		// Check if PID belongs to this container via cgroup
		cgroupPath := fmt.Sprintf("/proc/%d/cgroup", pid)
		data, err := os.ReadFile(cgroupPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), containerID) {
			return pid
		}

		// Also check if PID is a child of init process
		ppidPath := fmt.Sprintf("/proc/%d/stat", pid)
		ppidData, err := os.ReadFile(ppidPath)
		if err != nil {
			continue
		}
		// Parse PPID from stat file (4th field)
		fields := strings.Fields(string(ppidData))
		if len(fields) > 3 {
			var ppid int
			fmt.Sscanf(fields[3], "%d", &ppid)
			if ppid == initPID {
				return pid
			}
		}
	}
	return 0
}

func cudaCheckpoint(pid int) error {
	ckpt, err := cuda.NewCheckpointer()
	if err != nil {
		return err
	}

	state, err := ckpt.GetState(pid)
	if err != nil {
		return err
	}

	if state != cuda.StateRunning {
		return fmt.Errorf("process not in running state: %s", state)
	}

	return ckpt.CheckpointFull(pid, 60000)
}

func cudaRestore(pid int) error {
	ckpt, err := cuda.NewCheckpointer()
	if err != nil {
		return err
	}

	state, err := ckpt.GetState(pid)
	if err != nil {
		return err
	}

	if state != cuda.StateCheckpointed {
		return fmt.Errorf("process not in checkpointed state: %s", state)
	}

	return ckpt.RestoreFull(pid)
}

func criuCheckpoint(containerID, checkpointPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sudo", "/snap/microk8s/current/bin/runc",
		"--root", "/run/containerd/runc/k8s.io",
		"checkpoint",
		"--image-path", checkpointPath,
		"--leave-running",
		containerID,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, output)
	}
	return nil
}

func criuRestore(checkpointPath, namespace, pod, container string) (string, int, error) {
	// CRIU restore in Kubernetes context requires CRI restore API
	// which is not yet widely available.
	//
	// Current options:
	// 1. Create new pod with checkpoint annotation (requires controller)
	// 2. Use runc restore directly (requires bundle)
	// 3. Use buildah/podman to create OCI image from checkpoint

	return "", 0, fmt.Errorf("CRI restore not yet implemented - use pod recreation with checkpoint")
}
