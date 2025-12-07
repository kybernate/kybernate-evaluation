# GPU Restore Fix: NVIDIA Hook Mounts

## Problem Description

When restoring a GPU-enabled container using CRIU (via `runc restore`), the operation failed with the following error:

```
Error (criu/mount.c:3141): mnt: No mapping for 9337:(null) mountpoint
```

### Root Cause Analysis

1.  **NVIDIA Container Toolkit Hooks**:
    The NVIDIA Container Toolkit uses OCI hooks (specifically `nvidia-ctk`) to inject the NVIDIA driver and libraries into the container.
    To do this, it creates a temporary `tmpfs` mount at a dynamically generated path, for example:
    `/run/nvidia-ctk-hookeb76720e-f744-46a6-abec-85fd6935d5e3`

2.  **CRIU Checkpoint**:
    When CRIU checkpoints the container, it captures the mount namespace, including this temporary `tmpfs` mount point.

3.  **Restore Failure**:
    During restore, CRIU attempts to reconstruct the mount namespace. It expects the mount point directory (e.g., `/run/nvidia-ctk-hook...`) to exist in the rootfs so it can mount the restored `tmpfs` onto it.
    However, since we are restoring into a new container (or the hook hasn't run yet, or ran with a *different* random UUID), the specific directory from the checkpointed session does not exist.
    CRIU fails because it cannot find the mount point to restore the mount.

## Solution

The solution involves detecting these ephemeral directories during checkpoint and recreating them before restore.

### 1. Detection (Checkpoint Phase)

In `shim/pkg/cuda/detect.go`, we added `FindNvidiaHookMounts(pid int)` which scans `/proc/<pid>/mountinfo` for any mount points containing `nvidia-ctk-hook`.

In `shim/pkg/service/service.go` (`Checkpoint` method), we call this function and save the detected paths to a file named `nvidia-hooks.json` in the checkpoint directory.

### 2. Restoration (Create Phase)

In `shim/pkg/service/service.go` (`Create` method), when a restore operation is detected:
1.  We check for the existence of `nvidia-hooks.json` in the checkpoint directory.
2.  If found, we read the list of paths.
3.  We manually create these directories in the container's `rootfs` (e.g., `<bundle>/rootfs/run/nvidia-ctk-hook...`).

This ensures that when `runc restore` hands over to CRIU, the required mount points exist, allowing CRIU to successfully restore the mount namespace.

## Verification

This fix was verified by manually reproducing the failure with `runc restore` and confirming that creating the missing directory resolved the `mnt: No mapping` error.
