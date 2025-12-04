package cuda

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// GPUProcess represents a process using the GPU
type GPUProcess struct {
	PID        int
	UsedMemory int64 // in bytes
	Name       string
}

// FindGPUProcesses returns all processes currently using the GPU
func FindGPUProcesses() ([]GPUProcess, error) {
	// Use nvidia-smi to query GPU processes
	cmd := exec.Command("nvidia-smi",
		"--query-compute-apps=pid,used_memory,process_name",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi failed: %w", err)
	}

	var processes []GPUProcess
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, ", ")
		if len(parts) < 2 {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}

		// Memory is in MiB from nvidia-smi
		memMiB, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)

		name := ""
		if len(parts) >= 3 {
			name = strings.TrimSpace(parts[2])
		}

		processes = append(processes, GPUProcess{
			PID:        pid,
			UsedMemory: memMiB * 1024 * 1024, // Convert to bytes
			Name:       name,
		})
	}

	return processes, nil
}

// FindGPUProcessForContainer finds a GPU process that belongs to a specific container
// by checking cgroup membership
func FindGPUProcessForContainer(containerID string) (int, bool) {
	processes, err := FindGPUProcesses()
	if err != nil || len(processes) == 0 {
		return 0, false
	}

	for _, proc := range processes {
		if belongsToContainer(proc.PID, containerID) {
			return proc.PID, true
		}
	}

	return 0, false
}

// FindAnyGPUProcessForTask finds a GPU process that belongs to a specific containerd task
// by checking the process tree
func FindAnyGPUProcessForTask(taskPID int) (int, bool) {
	processes, err := FindGPUProcesses()
	if err != nil || len(processes) == 0 {
		return 0, false
	}

	for _, proc := range processes {
		if isDescendant(proc.PID, taskPID) {
			return proc.PID, true
		}
	}

	return 0, false
}

// belongsToContainer checks if a process belongs to a container by checking cgroup
func belongsToContainer(pid int, containerID string) bool {
	cgroupPath := filepath.Join("/proc", strconv.Itoa(pid), "cgroup")
	data, err := os.ReadFile(cgroupPath)
	if err != nil {
		return false
	}

	// Container ID should appear in cgroup path
	return strings.Contains(string(data), containerID)
}

// isDescendant checks if childPID is a descendant of parentPID
func isDescendant(childPID, parentPID int) bool {
	if childPID == parentPID {
		return true
	}

	// Walk up the process tree
	currentPID := childPID
	for currentPID > 1 {
		statPath := filepath.Join("/proc", strconv.Itoa(currentPID), "stat")
		data, err := os.ReadFile(statPath)
		if err != nil {
			return false
		}

		// Parse ppid from /proc/pid/stat
		// Format: pid (comm) state ppid ...
		fields := strings.Fields(string(data))
		if len(fields) < 4 {
			return false
		}

		// Find the closing paren of comm, ppid is right after
		statStr := string(data)
		closeParen := strings.LastIndex(statStr, ")")
		if closeParen == -1 || closeParen+2 >= len(statStr) {
			return false
		}

		afterComm := strings.Fields(statStr[closeParen+2:])
		if len(afterComm) < 2 {
			return false
		}

		ppid, err := strconv.Atoi(afterComm[1])
		if err != nil {
			return false
		}

		if ppid == parentPID {
			return true
		}

		currentPID = ppid
	}

	return false
}

// HasGPU checks if any GPU is available in the system
func HasGPU() bool {
	_, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return false
	}

	cmd := exec.Command("nvidia-smi", "-L")
	return cmd.Run() == nil
}
