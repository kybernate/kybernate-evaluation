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

// FindNvidiaMounts finds all NVIDIA-related mounts by inspecting /proc/<pid>/mountinfo
// It returns a list of mount details (source, destination, options)
func FindNvidiaMounts(pid int) ([]MountInfo, error) {
	mountinfoPath := fmt.Sprintf("/proc/%d/mountinfo", pid)
	file, err := os.Open(mountinfoPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var mounts []MountInfo
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		root := fields[3]
		mountPoint := fields[4]
		mountOptions := fields[5]

		// Find the separator "-"
		sepIndex := -1
		for i, f := range fields {
			if f == "-" {
				sepIndex = i
				break
			}
		}

		if sepIndex == -1 || sepIndex+2 >= len(fields) {
			continue
		}

		fsType := fields[sepIndex+1]

		// Check for NVIDIA keywords
		isNvidia := false
		keywords := []string{"nvidia", "cuda", "libnv", "gsp_"}
		for _, kw := range keywords {
			if strings.Contains(mountPoint, kw) || strings.Contains(root, kw) {
				isNvidia = true
				break
			}
		}

		// Skip pseudo-filesystems and overlay
		// We only want bind mounts of files/directories from the host
		// However, some NVIDIA mounts (like sockets or params) might be tmpfs
		if fsType == "proc" || fsType == "sysfs" ||
			fsType == "cgroup" || fsType == "cgroup2" || fsType == "devtmpfs" ||
			fsType == "devpts" || fsType == "mqueue" || fsType == "overlay" {
			continue
		}

		// Skip mounts inside /proc (e.g. /proc/driver/nvidia/params) to avoid runc safety errors
		if strings.HasPrefix(mountPoint, "/proc/") {
			continue
		}

		if !isNvidia && fsType == "tmpfs" {
			continue
		}

		if isNvidia {
			// Construct source path.
			// For bind mounts from the host, 'root' is the path on the host filesystem.
			// However, for tmpfs mounts (like /run/nvidia-persistenced/socket), 'root' is relative to the tmpfs root.
			// In those cases, the mount point usually matches the host path (1:1 mapping).
			source := root
			mountType := "bind"

			if fsType == "tmpfs" {
				if root == "/" {
					// This is a tmpfs mount (not a bind mount of a file on tmpfs)
					// e.g. /run/nvidia-ctk-hook...
					// We should restore it as a new tmpfs
					mountType = "tmpfs"
					source = "tmpfs"
				} else {
					// This is a bind mount of a file/dir on a tmpfs
					// e.g. /run/nvidia-persistenced/socket
					// Use destination as source because we can't resolve the host path from 'root'
					source = mountPoint
				}
			}

			// Options: split by comma
			opts := strings.Split(mountOptions, ",")
			// Add "bind" if it's a bind mount?
			// CRIU expects bind mounts to be specified.
			// runc expects "bind" type.

			mounts = append(mounts, MountInfo{
				Source:      source,
				Destination: mountPoint,
				Type:        mountType, // Most NVIDIA mounts are bind mounts
				Options:     opts,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return mounts, nil
}

// MountInfo represents a mount point
type MountInfo struct {
	Source      string
	Destination string
	Type        string
	Options     []string
}

// FindNvidiaHookMounts finds any mount points related to NVIDIA hooks (e.g. /run/nvidia-ctk-hook*)
// by inspecting /proc/<pid>/mountinfo
func FindNvidiaHookMounts(pid int) ([]string, error) {
	mountinfoPath := fmt.Sprintf("/proc/%d/mountinfo", pid)
	file, err := os.Open(mountinfoPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hookPaths []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
		// We are interested in the mount point (5th field)
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountPoint := fields[4]

		if strings.Contains(mountPoint, "nvidia-ctk-hook") {
			hookPaths = append(hookPaths, mountPoint)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return hookPaths, nil
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
