// Package runtime provides OCI runtime abstraction for kybernate shim.
// It reads runtime options from the bundle and uses the appropriate OCI runtime binary.
package runtime

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// DefaultRuntime is the default OCI runtime binary to use
const DefaultRuntime = "runc"

// Options represents the runc runtime options from options.json
type Options struct {
	BinaryName    string `json:"binary_name,omitempty"`
	Root          string `json:"root,omitempty"`
	SystemdCgroup bool   `json:"systemd_cgroup,omitempty"`
	NoPivotRoot   bool   `json:"no_pivot_root,omitempty"`
	NoNewKeyring  bool   `json:"no_new_keyring,omitempty"`
	CriuImagePath string `json:"criu_image_path,omitempty"`
	CriuWorkPath  string `json:"criu_work_path,omitempty"`
	IoUid         uint32 `json:"io_uid,omitempty"`
	IoGid         uint32 `json:"io_gid,omitempty"`
	ShimCgroup    string `json:"shim_cgroup,omitempty"`
}

// ReadOptions reads runtime options from the bundle's options.json file
func ReadOptions(bundlePath string) (*Options, error) {
	optionsPath := filepath.Join(bundlePath, "options.json")

	data, err := os.ReadFile(optionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Options{}, nil
		}
		return nil, err
	}

	var opts Options
	if err := json.Unmarshal(data, &opts); err != nil {
		return nil, err
	}

	return &opts, nil
}

// GetRuntimeBinary returns the OCI runtime binary to use.
// It first checks options.json, then falls back to the default.
func GetRuntimeBinary(bundlePath string) string {
	opts, err := ReadOptions(bundlePath)
	if err != nil {
		return DefaultRuntime
	}

	if opts.BinaryName != "" {
		return opts.BinaryName
	}

	return DefaultRuntime
}

// IsNvidiaRuntime checks if the runtime is nvidia-container-runtime
func IsNvidiaRuntime(binary string) bool {
	return binary == "nvidia-container-runtime" ||
		binary == "/usr/bin/nvidia-container-runtime"
}

// RuntimeExists checks if the given runtime binary exists and is executable
func RuntimeExists(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

// WriteOptions writes the runtime options to the bundle's options.json file
func WriteOptions(bundlePath string, opts *Options) error {
	optionsPath := filepath.Join(bundlePath, "options.json")

	data, err := json.Marshal(opts)
	if err != nil {
		return err
	}

	return os.WriteFile(optionsPath, data, 0600)
}
