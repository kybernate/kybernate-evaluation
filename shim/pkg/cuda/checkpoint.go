// Package cuda provides Go bindings for the NVIDIA CUDA Checkpoint API.
// This enables GPU memory checkpointing without the cuda-checkpoint binary.
//
// Reference: https://docs.nvidia.com/cuda/cuda-driver-api/group__CUDA__CHECKPOINT.html
package cuda

/*
#cgo LDFLAGS: -lcuda
#cgo CFLAGS: -I/usr/local/cuda/include

#include <cuda.h>
#include <stdlib.h>

// Wrapper functions for Go - using types from cuda.h

static CUresult cuda_init() {
    return cuInit(0);
}

static CUresult cuda_checkpoint_lock(int pid, unsigned int timeout_ms) {
    CUcheckpointLockArgs args = {0};
    args.timeoutMs = timeout_ms;
    return cuCheckpointProcessLock(pid, &args);
}

static CUresult cuda_checkpoint_checkpoint(int pid) {
    CUcheckpointCheckpointArgs args = {0};
    return cuCheckpointProcessCheckpoint(pid, &args);
}

static CUresult cuda_checkpoint_restore(int pid) {
    CUcheckpointRestoreArgs args = {0};
    args.gpuPairsCount = 0;
    args.gpuPairs = NULL;
    return cuCheckpointProcessRestore(pid, &args);
}

static CUresult cuda_checkpoint_unlock(int pid) {
    CUcheckpointUnlockArgs args = {0};
    return cuCheckpointProcessUnlock(pid, &args);
}

static CUresult cuda_checkpoint_get_state(int pid, int* state) {
    CUprocessState s;
    CUresult result = cuCheckpointProcessGetState(pid, &s);
    *state = (int)s;
    return result;
}
*/
import "C"

import (
	"fmt"
)

// ProcessState represents the CUDA checkpoint state of a process
type ProcessState int

const (
	StateRunning      ProcessState = 0
	StateLocked       ProcessState = 1
	StateCheckpointed ProcessState = 2
)

func (s ProcessState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateLocked:
		return "locked"
	case StateCheckpointed:
		return "checkpointed"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// CUDAError wraps a CUDA error code
type CUDAError struct {
	Code    int
	Message string
}

func (e *CUDAError) Error() string {
	return fmt.Sprintf("CUDA error %d: %s", e.Code, e.Message)
}

// cudaError converts a CUresult to a Go error
func cudaError(result C.CUresult, operation string) error {
	if result == C.CUDA_SUCCESS {
		return nil
	}
	return &CUDAError{
		Code:    int(result),
		Message: fmt.Sprintf("%s failed", operation),
	}
}

// Checkpointer provides GPU checkpoint/restore functionality
type Checkpointer struct {
	initialized bool
}

// NewCheckpointer creates a new CUDA checkpointer
func NewCheckpointer() (*Checkpointer, error) {
	result := C.cuda_init()
	if err := cudaError(result, "cuInit"); err != nil {
		return nil, err
	}
	return &Checkpointer{initialized: true}, nil
}

// GetState returns the current checkpoint state of a process
func (c *Checkpointer) GetState(pid int) (ProcessState, error) {
	var state C.int
	result := C.cuda_checkpoint_get_state(C.int(pid), &state)
	if err := cudaError(result, "cuCheckpointProcessGetState"); err != nil {
		return StateRunning, err
	}
	return ProcessState(state), nil
}

// Lock locks a CUDA process, blocking further CUDA API calls
// timeoutMs specifies the timeout in milliseconds (0 = no timeout)
func (c *Checkpointer) Lock(pid int, timeoutMs uint) error {
	result := C.cuda_checkpoint_lock(C.int(pid), C.uint(timeoutMs))
	return cudaError(result, "cuCheckpointProcessLock")
}

// Checkpoint moves GPU memory contents to host memory
// The process must be in LOCKED state
func (c *Checkpointer) Checkpoint(pid int) error {
	result := C.cuda_checkpoint_checkpoint(C.int(pid))
	return cudaError(result, "cuCheckpointProcessCheckpoint")
}

// Restore moves host memory contents back to GPU memory
// The process must be in CHECKPOINTED state
func (c *Checkpointer) Restore(pid int) error {
	result := C.cuda_checkpoint_restore(C.int(pid))
	return cudaError(result, "cuCheckpointProcessRestore")
}

// Unlock unlocks a CUDA process, allowing CUDA API calls
// The process must be in LOCKED state
func (c *Checkpointer) Unlock(pid int) error {
	result := C.cuda_checkpoint_unlock(C.int(pid))
	return cudaError(result, "cuCheckpointProcessUnlock")
}

// CheckpointFull performs a complete VRAM → Host RAM checkpoint
// Returns the process to CHECKPOINTED state with VRAM freed
func (c *Checkpointer) CheckpointFull(pid int, timeoutMs uint) error {
	// Step 1: Lock the process
	if err := c.Lock(pid, timeoutMs); err != nil {
		return fmt.Errorf("lock failed: %w", err)
	}

	// Step 2: Checkpoint VRAM to RAM
	if err := c.Checkpoint(pid); err != nil {
		// Try to unlock on failure
		_ = c.Unlock(pid)
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	return nil
}

// RestoreFull performs a complete Host RAM → VRAM restore
// Returns the process to RUNNING state with VRAM restored
func (c *Checkpointer) RestoreFull(pid int) error {
	// Step 1: Restore VRAM from RAM
	if err := c.Restore(pid); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	// Step 2: Unlock the process
	if err := c.Unlock(pid); err != nil {
		return fmt.Errorf("unlock failed: %w", err)
	}

	return nil
}
