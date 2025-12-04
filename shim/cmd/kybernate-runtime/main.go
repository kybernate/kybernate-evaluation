// Package main implements the kybernate-runtime OCI wrapper.
// This binary acts as a drop-in replacement for runc/nvidia-container-runtime
// and adds CUDA checkpoint/restore hooks for GPU workloads.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kybernate/kybernate/pkg/cuda"
)

const (
	// DefaultRuntime is the OCI runtime to delegate to
	DefaultRuntime = "nvidia-container-runtime"

	// FallbackRuntime is used if nvidia-container-runtime is not available
	FallbackRuntime = "runc"

	// LogFile for debugging
	LogFile = "/tmp/kybernate-runtime.log"
)

func main() {
	// Find the underlying runtime
	runtime := findRuntime()

	// Parse args to detect checkpoint/restore commands
	args := os.Args[1:]

	// Check for checkpoint command
	if containsArg(args, "checkpoint") {
		handleCheckpoint(runtime, args)
		return
	}

	// Check for restore command
	if containsArg(args, "restore") {
		handleRestore(runtime, args)
		return
	}

	// For all other commands, delegate directly
	execRuntime(runtime, args)
}

// findRuntime returns the path to the underlying OCI runtime
func findRuntime() string {
	// Check for nvidia-container-runtime first
	if path, err := exec.LookPath(DefaultRuntime); err == nil {
		return path
	}

	// Fall back to runc
	if path, err := exec.LookPath(FallbackRuntime); err == nil {
		return path
	}

	// Last resort - use absolute paths
	for _, path := range []string{
		"/usr/bin/nvidia-container-runtime",
		"/usr/bin/runc",
		"/usr/sbin/runc",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	fatal("no OCI runtime found")
	return ""
}

// handleCheckpoint intercepts checkpoint commands to perform CUDA checkpoint
func handleCheckpoint(runtime string, args []string) {
	debugLog("Checkpoint command detected")

	// For checkpoint, we get the container ID as the last argument
	containerID := ""
	if len(args) > 0 {
		containerID = args[len(args)-1]
	}

	// Find root path for container state
	rootPath := findRootArg(args)
	if rootPath == "" {
		rootPath = "/run/containerd/runc/k8s.io"
	}

	// Get container PID from state
	pid := findContainerPIDFromState(rootPath, containerID)
	if pid > 0 {
		debugLog(fmt.Sprintf("Found container init PID %d for checkpoint", pid))

		// Find GPU process (may be the init process or a child)
		gpuPID := findGPUProcessPID(pid)
		if gpuPID > 0 {
			debugLog(fmt.Sprintf("GPU process detected (PID %d), performing CUDA checkpoint", gpuPID))

			// Perform CUDA checkpoint before CRIU
			if err := cudaCheckpoint(gpuPID); err != nil {
				debugLog(fmt.Sprintf("CUDA checkpoint failed: %v (continuing with CRIU)", err))
			} else {
				debugLog("CUDA checkpoint successful - VRAM transferred to RAM")
			}
		} else {
			debugLog("Not a GPU process, skipping CUDA checkpoint")
		}
	} else {
		debugLog(fmt.Sprintf("Could not find PID for container %s", containerID))
	}

	// Delegate to actual runtime
	execRuntime(runtime, args)
}

// handleRestore intercepts restore commands to perform CUDA restore
func handleRestore(runtime string, args []string) {
	debugLog("Restore command detected")

	// For restore, we need to call the runtime first, then restore CUDA state
	// This is more complex because we need the PID after restore

	// Delegate to actual runtime
	// Note: CUDA restore needs to happen after the process is running
	// This might need to be done via a post-restore hook
	execRuntime(runtime, args)
}

// findBundleArg extracts the bundle path from command args
func findBundleArg(args []string) string {
	for i, arg := range args {
		if arg == "--bundle" || arg == "-b" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(arg, "--bundle=") {
			return strings.TrimPrefix(arg, "--bundle=")
		}
	}
	return ""
}

// findRootArg extracts the root path from command args
func findRootArg(args []string) string {
	for i, arg := range args {
		if arg == "--root" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(arg, "--root=") {
			return strings.TrimPrefix(arg, "--root=")
		}
	}
	return ""
}

// findContainerPIDFromState reads the container PID from runc state
func findContainerPIDFromState(rootPath, containerID string) int {
	// runc stores state in {root}/{containerID}/state.json
	statePath := filepath.Join(rootPath, containerID, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		debugLog(fmt.Sprintf("Failed to read state.json: %v", err))
		return 0
	}

	var state struct {
		Init int `json:"init_process_pid"`
		Pid  int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		debugLog(fmt.Sprintf("Failed to parse state.json: %v", err))
		return 0
	}

	if state.Init > 0 {
		return state.Init
	}
	return state.Pid
}

// isGPUProcess checks if a process is using GPU
func isGPUProcess(pid int) bool {
	return findGPUProcessPID(pid) > 0
}

// findGPUProcessPID finds the actual GPU process PID (may be a child of the given pid)
func findGPUProcessPID(pid int) int {
	// Check nvidia-smi for this PID and its children
	cmd := exec.Command("nvidia-smi", "--query-compute-apps=pid", "--format=csv,noheader")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	gpuPids := make(map[int]bool)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var gpuPid int
		fmt.Sscanf(line, "%d", &gpuPid)
		if gpuPid > 0 {
			gpuPids[gpuPid] = true
		}
	}

	// Check if the init pid itself is a GPU process
	if gpuPids[pid] {
		return pid
	}

	// Check child processes recursively
	return findGPUChild(pid, gpuPids)
}

// findGPUChild recursively searches for a GPU process in the process tree
func findGPUChild(pid int, gpuPids map[int]bool) int {
	childPids := getChildPIDs(pid)
	for _, childPid := range childPids {
		if gpuPids[childPid] {
			return childPid
		}
		// Recursively check grandchildren
		if found := findGPUChild(childPid, gpuPids); found > 0 {
			return found
		}
	}
	return 0
}

// getChildPIDs returns all child PIDs of a process
func getChildPIDs(pid int) []int {
	var children []int
	childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	data, err := os.ReadFile(childrenPath)
	if err != nil {
		return children
	}

	for _, pidStr := range strings.Fields(string(data)) {
		var childPid int
		fmt.Sscanf(pidStr, "%d", &childPid)
		if childPid > 0 {
			children = append(children, childPid)
			// Recursively get grandchildren
			children = append(children, getChildPIDs(childPid)...)
		}
	}
	return children
}

// isGPUContainer checks if the container has GPU resources
func isGPUContainer(bundlePath string) bool {
	configPath := filepath.Join(bundlePath, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}

	// Simple check: look for NVIDIA environment variables
	config := string(data)
	return strings.Contains(config, "NVIDIA_VISIBLE_DEVICES") ||
		strings.Contains(config, "nvidia.com/gpu")
}

// findContainerPID reads the container init PID
func findContainerPID(bundlePath string) int {
	pidFile := filepath.Join(bundlePath, "init.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}

	var pid int
	fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
	return pid
}

// cudaCheckpoint performs CUDA checkpoint using the cuda package
func cudaCheckpoint(pid int) error {
	debugLog(fmt.Sprintf("Performing CUDA checkpoint for PID %d", pid))

	// Create checkpointer
	ckpt, err := cuda.NewCheckpointer()
	if err != nil {
		return fmt.Errorf("failed to create checkpointer: %w", err)
	}

	// Get current state
	state, err := ckpt.GetState(pid)
	if err != nil {
		return fmt.Errorf("failed to get process state: %w", err)
	}
	debugLog(fmt.Sprintf("Current process state: %s", state))

	// Perform full checkpoint (Lock + Checkpoint)
	// Use 30 second timeout
	if err := ckpt.CheckpointFull(pid, 30000); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	debugLog(fmt.Sprintf("CUDA checkpoint successful for PID %d - VRAM transferred to RAM", pid))
	return nil
}

// containsArg checks if args contain a specific argument
func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

// execRuntime replaces the current process with the runtime
func execRuntime(runtime string, args []string) {
	allArgs := append([]string{runtime}, args...)

	debugLog(fmt.Sprintf("Executing: %s %v", runtime, args))

	// Use syscall.Exec to replace the current process
	if err := syscall.Exec(runtime, allArgs, os.Environ()); err != nil {
		fatal(fmt.Sprintf("failed to exec %s: %v", runtime, err))
	}
}

func debugLog(msg string) {
	f, err := os.OpenFile(LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	data, _ := json.Marshal(map[string]string{
		"msg":  msg,
		"args": strings.Join(os.Args, " "),
	})
	f.WriteString(string(data) + "\n")
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "kybernate-runtime: %s\n", msg)
	os.Exit(1)
}
