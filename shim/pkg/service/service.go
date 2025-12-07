package service

import (
	"bufio"
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
	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	runc "github.com/containerd/containerd/runtime/v2/runc/v2"
	"github.com/containerd/containerd/runtime/v2/shim"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/protobuf/types/known/anypb"
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

		// Debug: Copy config.json for manual reproduction
		if data, err := os.ReadFile(configPath); err == nil {
			os.WriteFile("/tmp/last-config.json", data, 0644)
			debugLog("Copied config.json to /tmp/last-config.json")
		}

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
				// We enable this for restore as well, so that NVIDIA driver mounts (libraries, etc.)
				// are injected into the config.json. CRIU needs these mounts to match the checkpoint.
				// UPDATE: For restore, we manually inject mounts from the checkpoint.
				// Using nvidia-container-runtime might cause conflicts or double injection.
				// So we ONLY use it for non-restore workloads.
				if hasGPUResources(spec) && !isRestore {
					// Try to modify req.Options (protobuf)
					if req.Options != nil {
						v, err := req.Options.UnmarshalNew()
						if err == nil {
							if opts, ok := v.(*runcoptions.Options); ok {
								opts.BinaryName = "nvidia-container-runtime"
								newOpts, err := anypb.New(opts)
								if err == nil {
									req.Options = newOpts
									debugLog("Switched runtime binary to nvidia-container-runtime via protobuf")
								} else {
									debugLog(fmt.Sprintf("Failed to marshal new options: %v", err))
								}
							}
						} else {
							debugLog(fmt.Sprintf("Failed to unmarshal options: %v", err))
						}
					} else {
						// Create new options if nil
						opts := &runcoptions.Options{
							BinaryName: "nvidia-container-runtime",
						}
						newOpts, err := anypb.New(opts)
						if err == nil {
							req.Options = newOpts
							debugLog("Created new options with nvidia-container-runtime")
						} else {
							debugLog(fmt.Sprintf("Failed to marshal new options (from nil): %v", err))
						}
					}

					// Also try the options.json method as fallback/complement
					if err := ensureNvidiaRuntime(req.Bundle, spec); err != nil {
						debugLog(fmt.Sprintf("Failed to set nvidia-container-runtime via options.json: %v", err))
					}
				}

				// If restoring, check for saved NVIDIA mounts and inject them
				if isRestore && checkpointPath != "" {
					mountsFile := filepath.Join(checkpointPath, "nvidia-mounts.json")
					if data, err := os.ReadFile(mountsFile); err == nil {
						var nvidiaMounts []cuda.MountInfo
						if err := json.Unmarshal(data, &nvidiaMounts); err == nil {
							debugLog(fmt.Sprintf("Injecting %d NVIDIA mounts from checkpoint", len(nvidiaMounts)))

							// Add to spec.Mounts
							for _, m := range nvidiaMounts {
								// Check if already exists (to avoid duplicates)
								exists := false
								for _, existing := range spec.Mounts {
									if existing.Destination == m.Destination {
										exists = true
										break
									}
								}

								if !exists {
									spec.Mounts = append(spec.Mounts, specs.Mount{
										Source:      m.Source,
										Destination: m.Destination,
										Type:        m.Type,
										Options:     m.Options,
									})
									debugLog(fmt.Sprintf("Injected mount: %s -> %s", m.Source, m.Destination))
								}
							}

							// Write updated spec back to config.json
							if newData, err := json.Marshal(spec); err == nil {
								if err := os.WriteFile(configPath, newData, 0644); err != nil {
									debugLog(fmt.Sprintf("Failed to write updated config.json: %v", err))
								} else {
									debugLog("Updated config.json with injected mounts")
									// Save a copy for debugging in a shared directory
									debugPath := "/var/snap/microk8s/common/run/debug-config.json"
									os.WriteFile(debugPath, newData, 0644)
									debugLog(fmt.Sprintf("Wrote debug config to %s", debugPath))
								}
							}
							// Also ensure directories/files exist for these mounts
							rootfs := filepath.Join(req.Bundle, "rootfs")
							for _, m := range nvidiaMounts {
								relPath := strings.TrimPrefix(m.Destination, "/")
								fullPath := filepath.Join(rootfs, relPath)

								// Check source on host to determine if dir or file
								fi, err := os.Stat(m.Source)
								if err == nil {
									if fi.IsDir() {
										os.MkdirAll(fullPath, 0755)
									} else {
										// Ensure parent dir exists
										os.MkdirAll(filepath.Dir(fullPath), 0755)
										// Create empty file if not exists
										f, err := os.OpenFile(fullPath, os.O_RDONLY|os.O_CREATE, 0644)
										if err == nil {
											f.Close()
										}
									}
								} else {
									// Fallback: assume directory if no extension?
									if strings.Contains(filepath.Base(m.Destination), ".") {
										os.MkdirAll(filepath.Dir(fullPath), 0755)
										f, err := os.OpenFile(fullPath, os.O_RDONLY|os.O_CREATE, 0644)
										if err == nil {
											f.Close()
										}
									} else {
										os.MkdirAll(fullPath, 0755)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Call the underlying shim to create/restore the container
	resp, err := s.Shim.Create(ctx, req)
	if err != nil {
		return resp, err
	}

	candidateIDs := []string{}
	candidateIDs = appendCandidate(candidateIDs, req.ID)
	if bundleID := filepath.Base(req.Bundle); bundleID != "" {
		candidateIDs = appendCandidate(candidateIDs, bundleID)
	}

	if spec != nil && spec.Annotations != nil {
		if sid, ok := spec.Annotations["io.kubernetes.cri.sandbox-id"]; ok {
			candidateIDs = appendCandidate(candidateIDs, sid)
		}
	}

	candidateIDs = expandCandidatePrefixes(candidateIDs)

	// If this was a restore and we have GPU support, perform CUDA restore
	if isRestore && s.cudaCheckpointer != nil {
		debugLog(fmt.Sprintf("Checking for GPU process to restore (checkpoint: %s)", checkpointPath))

		// Wait a moment for the process to start
		time.Sleep(500 * time.Millisecond)

		restored := false
		initPID := int(resp.Pid)

		// If resp.Pid is 0 (which happens with some runtimes/restore flows), try to resolve it from the bundle
		if initPID <= 0 {
			debugLog(fmt.Sprintf("resp.Pid was %d, trying to resolve from bundle: %s", initPID, req.Bundle))

			for i := 0; i < 75; i++ { // Retry for up to ~15 seconds
				// 1. Try init.pid in the bundle directory (most reliable)
				pidPath := filepath.Join(req.Bundle, "init.pid")
				data, err := os.ReadFile(pidPath)
				if err == nil {
					pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
					if err == nil && pid > 0 {
						initPID = pid
						debugLog(fmt.Sprintf("Resolved PID %d from %s after retry %d", initPID, pidPath, i))
						break
					}
				} else {
					if i%10 == 0 {
						debugLog(fmt.Sprintf("Failed to read %s: %v", pidPath, err))
						// List directory contents to debug
						entries, _ := os.ReadDir(req.Bundle)
						var names []string
						for _, e := range entries {
							names = append(names, e.Name())
						}
						debugLog(fmt.Sprintf("Bundle contents: %v", names))
					}
				}

				// 2. Fallback: Try getTaskPID which checks standard locations
				if pid := s.getTaskPID(candidateIDs...); pid > 0 {
					initPID = pid
					debugLog(fmt.Sprintf("Resolved PID %d from getTaskPID after retry %d", initPID, i))
					break
				}

				// 3. Deep Fallback: Scan cgroups (expensive but necessary for some runtimes)
				if i > 5 { // Only try this after a few quick failures
					debugLog(fmt.Sprintf("Running cgroup scan with candidates: %v", candidateIDs))
					if pid := s.findPIDFromCgroup(candidateIDs); pid > 0 {
						initPID = pid
						debugLog(fmt.Sprintf("Resolved PID %d from cgroup scan after retry %d", initPID, i))
						break
					}

					// 4. Shim parent: if we can find the shim for this container, use its child as init PID
					debugLog(fmt.Sprintf("Running shim-child scan with candidates: %v", candidateIDs))
					if pid := s.findPIDFromShim(candidateIDs); pid > 0 {
						initPID = pid
						debugLog(fmt.Sprintf("Resolved PID %d from shim child after retry %d", initPID, i))
						break
					}
				}

				time.Sleep(200 * time.Millisecond)
			}
		}

		// Try to check the init process directly first (nvidia-smi might not show it if VRAM is 0)
		if initPID > 0 {
			state, err := s.cudaCheckpointer.GetState(initPID)
			if err == nil && state == cuda.StateCheckpointed {
				debugLog(fmt.Sprintf("Found checkpointed process %d (init), performing CUDA restore", initPID))
				if err := s.cudaCheckpointer.RestoreFull(initPID); err != nil {
					debugLog(fmt.Sprintf("CUDA restore failed for PID %d: %v", initPID, err))
				} else {
					debugLog(fmt.Sprintf("CUDA restore successful for PID %d - VRAM restored", initPID))
					restored = true
				}
			} else {
				debugLog(fmt.Sprintf("Init process %d state: %s (err: %v)", initPID, state, err))
			}
		} else {
			// Do not fail container startup if we cannot resolve the PID yet.
			// This avoids breaking restore when the runtime hasn't written init.pid but
			// the process may still come up; we can rely on later detection/logging.
			debugLog("Could not resolve init PID – skipping CUDA restore but allowing container startup")
			return resp, nil
		}

		if !restored {
			// Fallback: Find GPU process in the restored container via nvidia-smi
			// Use initPID if available, otherwise 0 (which might fail)
			searchPID := initPID
			if searchPID == 0 {
				searchPID = int(resp.Pid)
			}

			if gpuPID, hasGPU := cuda.FindAnyGPUProcessForTask(searchPID); hasGPU {
				debugLog(fmt.Sprintf("Found GPU process %d via nvidia-smi, performing CUDA restore", gpuPID))

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

				state, err := s.cudaCheckpointer.GetState(gpuPID)
				if err != nil {
					debugLog(fmt.Sprintf("Failed to get CUDA state for PID %d: %v", gpuPID, err))
				} else if state == cuda.StateRunning {
					// Perform CUDA checkpoint with a shorter timeout to avoid long hangs
					if err := s.cudaCheckpointer.CheckpointFull(gpuPID, 10000); err != nil {
						debugLog(fmt.Sprintf("CUDA checkpoint failed or timed out for PID %d: %v (continuing with CRIU, GPU state may be lost)", gpuPID, err))
					} else {
						debugLog(fmt.Sprintf("CUDA checkpoint successful for PID %d - VRAM freed", gpuPID))
					}
				} else {
					debugLog(fmt.Sprintf("GPU process %d not in running state (state=%s), skipping CUDA checkpoint", gpuPID, state))
				}
			} else {
				debugLog("No GPU process found in container - CPU-only checkpoint")
			}

			// Detect and save all NVIDIA mounts (needed for restore)
			nvidiaMounts, err := cuda.FindNvidiaMounts(taskPID)
			if err == nil && len(nvidiaMounts) > 0 {
				debugLog(fmt.Sprintf("Found %d NVIDIA mounts", len(nvidiaMounts)))
				mountsFile := filepath.Join(req.Path, "nvidia-mounts.json")
				data, err := json.Marshal(nvidiaMounts)
				if err == nil {
					if err := os.WriteFile(mountsFile, data, 0644); err != nil {
						debugLog(fmt.Sprintf("Failed to write nvidia-mounts.json: %v", err))
					}
				} else {
					debugLog(fmt.Sprintf("Failed to marshal mounts: %v", err))
				}
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
func (s *Service) getTaskPID(containerIDs ...string) int {
	// Try multiple candidate IDs because bundle name and task ID can diverge on restore
	for _, containerID := range containerIDs {
		if containerID == "" {
			continue
		}

		// 1. Try reading PID from containerd's task state files
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

		// 2. Fallback: Try querying runc state directly
		// We try both standard runc and nvidia-container-runtime locations/binaries
		runcRoots := []string{
			"/run/containerd/runc/k8s.io",
			"/var/snap/microk8s/common/run/containerd/runc/k8s.io",
		}

		binaries := []string{"runc", "nvidia-container-runtime", "/snap/microk8s/current/bin/runc"}

		for _, root := range runcRoots {
			for _, bin := range binaries {
				cmd := exec.Command(bin, "--root", root, "state", containerID)
				output, err := cmd.Output()
				if err == nil {
					var state struct {
						InitProcessPID int `json:"init_process_pid"`
					}
					if err := json.Unmarshal(output, &state); err == nil && state.InitProcessPID > 0 {
						return state.InitProcessPID
					}
				}
			}
		}

		debugLog(fmt.Sprintf("Could not find init.pid for container %s", containerID))
	}

	return 0
}

func (s *Service) findPIDFromCgroup(containerIDs []string) int {
	// Scan /proc to find a process belonging to the container's cgroup
	// This is expensive but reliable if the runtime hides the PID
	// We look for cgroup paths containing any of the candidate container IDs

	procDirs, err := os.ReadDir("/proc")
	if err != nil {
		debugLog(fmt.Sprintf("Failed to read /proc: %v", err))
		return 0
	}

	for _, d := range procDirs {
		if !d.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}

		cgroupPath := filepath.Join("/proc", d.Name(), "cgroup")
		f, err := os.Open(cgroupPath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		found := false
		for scanner.Scan() {
			line := scanner.Text()
			for _, id := range containerIDs {
				if id != "" && strings.Contains(line, id) {
					found = true
					debugLog(fmt.Sprintf("Found PID %d in cgroup matching %s: %s", pid, id, line))
					break
				}
			}
			if found {
				break
			}
		}
		f.Close()

		if found {
			return pid
		}
	}
	return 0
}

func (s *Service) findPIDFromShim(containerIDs []string) int {
	// Find the shim process by its command line and return its first child PID (container init)
	procDirs, err := os.ReadDir("/proc")
	if err != nil {
		debugLog(fmt.Sprintf("Failed to read /proc for shim scan: %v", err))
		return 0
	}

	for _, d := range procDirs {
		if !d.IsDir() {
			continue
		}
		pid := d.Name()
		cmdlinePath := filepath.Join("/proc", pid, "cmdline")
		data, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		cmdline := strings.ReplaceAll(string(data), "\x00", " ")
		if !strings.Contains(cmdline, "containerd-shim") {
			continue
		}

		matched := false
		for _, id := range containerIDs {
			if id != "" && strings.Contains(cmdline, id) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		childrenPath := filepath.Join("/proc", pid, "task", pid, "children")
		childData, err := os.ReadFile(childrenPath)
		if err != nil {
			continue
		}

		fields := strings.Fields(string(childData))
		if len(fields) == 0 {
			continue
		}

		childPID, err := strconv.Atoi(fields[0])
		if err != nil || childPID <= 0 {
			continue
		}

		debugLog(fmt.Sprintf("Found init PID %d via shim %s (cmdline match)", childPID, pid))
		return childPID
	}

	return 0
}

func appendCandidate(list []string, id string) []string {
	if id == "" {
		return list
	}

	for _, existing := range list {
		if existing == id {
			return list
		}
	}

	return append(list, id)
}

func expandCandidatePrefixes(ids []string) []string {
	out := append([]string{}, ids...)

	for _, id := range ids {
		if len(id) > 12 {
			short := id[:12]
			out = appendCandidate(out, short)
		}
	}

	return out
}

func debugLog(msg string) {
	f, err := os.OpenFile("/var/snap/microk8s/common/run/kybernate-shim.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// Fallback to /tmp if the snap path is not accessible
		f, err = os.OpenFile("/tmp/kybernate-shim.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
	}
	defer f.Close()
	timestamp := time.Now().Format(time.RFC3339)
	f.WriteString(fmt.Sprintf("%s: %s\n", timestamp, msg))
}
