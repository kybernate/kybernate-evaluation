# Kybernate Containerd Shim

This directory contains the source code for the `containerd-shim-kybernate-v1`.
This shim acts as a proxy between containerd and runc, allowing us to intercept container lifecycle events for checkpoint/restore functionality.

## Architecture & Workflow

The shim acts as a "man-in-the-middle" between `containerd` and `runc`. It does not provide its own CLI but intercepts lifecycle commands.

### Checkpoint Flow
1.  **Trigger:** User/Operator runs `ctr task checkpoint`.
2.  **Containerd:** Forwards the request to the `kybernate` shim.
3.  **Shim:**
    *   Intercepts the `Checkpoint` call.
    *   Delegates to `runc checkpoint`.
    *   **CRIU:** `runc` invokes `criu dump` to save the process state to disk.
    *   **Post-Processing:** The shim copies the checkpoint data to a known location (e.g., `/tmp/kybernate-checkpoint`).

### Restore Flow
1.  **Trigger:** User/Operator runs `kubectl apply` (creates a new Pod).
2.  **Manifest:** The Pod spec includes a specific environment variable (e.g., `RESTORE_FROM=/tmp/checkpoint`).
3.  **Containerd:** Calls the shim's `Create` method.
4.  **Shim:**
    *   Intercepts `Create`.
    *   Detects the `RESTORE_FROM` environment variable.
    *   Modifies the `CreateTaskRequest` to include the checkpoint path.
5.  **Runc:** Receives a create request *with* a checkpoint path. Instead of starting a fresh process, it executes `runc restore`.
6.  **CRIU:** `runc` invokes `criu restore` to resurrect the process from the checkpoint files.

This architecture allows us to use standard Kubernetes workflows (like `kubectl apply`) to restore containers, while the shim handles the low-level complexity of instructing `runc` and `criu`.

## Prerequisites

*   Go 1.24+ (recommended: use the version specified in `go.mod`)
*   Linux environment (for building and running)
*   `sudo` privileges (for installation)
*   **CRIU** installed on the host (required by `runc` for checkpoint/restore)

## Build

To build the shim binary:

```bash
cd shim

# Install dependencies
go mod tidy

# Build binary
mkdir -p bin
go build -o bin/containerd-shim-kybernate-v1 ./cmd/containerd-shim-kybernate-v1
```

## Installation

We provide a script to automate the installation and configuration of containerd.

```bash
# Run the install script (requires sudo)
sudo ./scripts/install.sh
```

### Manual Installation

1.  **Copy Binary**:
    Copy the compiled binary to a directory in your `$PATH` (usually `/usr/local/bin`):
    ```bash
    sudo cp bin/containerd-shim-kybernate-v1 /usr/local/bin/
    sudo chmod +x /usr/local/bin/containerd-shim-kybernate-v1
    ```

2.  **Configure Containerd**:
    You need to register the `kybernate` runtime in your containerd configuration (`config.toml`).

    **For MicroK8s:**
    Edit `/var/snap/microk8s/current/args/containerd-template.toml`:
    ```toml
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate]
      runtime_type = "io.containerd.kybernate.v1"
    ```
    Then restart MicroK8s: `microk8s stop && microk8s start`.

    **For Standard Containerd (/etc/containerd/config.toml):**
    ```toml
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kybernate]
      runtime_type = "io.containerd.kybernate.v1"
    ```
    Then restart containerd: `sudo systemctl restart containerd`.

## Verification

To verify that the shim is correctly installed and integrated with Kubernetes, use the provided manifests.

### 1. Register RuntimeClass
Register the `kybernate` RuntimeClass in the cluster.
```bash
microk8s kubectl apply -f manifests/runtimeclass.yaml
```

### 2. CPU Checkpoint & Restore Test (Task 05)
We provide a complete walkthrough for testing CPU checkpoint and restore in `docs/CPU_CHECKPOINT.md`.

**Quick Summary:**

1.  **Deploy Counter Pod**:
    ```bash
    microk8s kubectl apply -f manifests/cpu-test-pod.yaml
    ```
2.  **Checkpoint**:
    Find the container ID and use `ctr` to checkpoint it. The shim will save it to `/tmp/kybernate-checkpoint`.
3.  **Restore**:
    Deploy the restore pod which reads from that location.
    ```bash
    microk8s kubectl apply -f manifests/cpu-restore-pod.yaml
    ```

### 3. Cleanup
```bash
microk8s kubectl delete pod cpu-test cpu-restore -n kybernate-system
```
