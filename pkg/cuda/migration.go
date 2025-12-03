// Package cuda - GPU UUID remapping for cross-node migration
package cuda

/*
#cgo LDFLAGS: -lcuda
#cgo CFLAGS: -I/usr/local/cuda/include

#include <cuda.h>
#include <string.h>

// Restore with GPU remapping
static CUresult cuda_checkpoint_restore_remap(int pid, 
    char* old_uuid, char* new_uuid) {
    
    CUcheckpointGpuPair pair;
    memcpy(pair.oldUuid.bytes, old_uuid, 16);
    memcpy(pair.newUuid.bytes, new_uuid, 16);
    
    CUcheckpointRestoreArgs args = {0};
    args.gpuPairsCount = 1;
    args.gpuPairs = &pair;
    
    return cuCheckpointProcessRestore(pid, &args);
}

// Get device UUID
static CUresult cuda_get_device_uuid(int device, char* uuid_out) {
    CUdevice dev;
    CUresult result = cuDeviceGet(&dev, device);
    if (result != CUDA_SUCCESS) return result;
    
    CUuuid uuid;
    result = cuDeviceGetUuid(&uuid, dev);
    if (result != CUDA_SUCCESS) return result;
    
    memcpy(uuid_out, uuid.bytes, 16);
    return CUDA_SUCCESS;
}

// Get device count
static CUresult cuda_get_device_count(int* count) {
    return cuDeviceGetCount(count);
}
*/
import "C"
import (
	"encoding/hex"
	"fmt"
	"unsafe"
)

// GPUInfo represents information about a GPU device
type GPUInfo struct {
	Index int
	UUID  [16]byte
}

// UUIDString returns the GPU UUID as a hex string
func (g *GPUInfo) UUIDString() string {
	return fmt.Sprintf("GPU-%s", hex.EncodeToString(g.UUID[:]))
}

// GetDeviceCount returns the number of CUDA devices
func (c *Checkpointer) GetDeviceCount() (int, error) {
	var count C.int
	result := C.cuda_get_device_count(&count)
	if err := cudaError(result, "cuDeviceGetCount"); err != nil {
		return 0, err
	}
	return int(count), nil
}

// GetDeviceUUID returns the UUID of a specific GPU device
func (c *Checkpointer) GetDeviceUUID(deviceIndex int) (*GPUInfo, error) {
	var uuid [16]C.char
	result := C.cuda_get_device_uuid(C.int(deviceIndex), &uuid[0])
	if err := cudaError(result, "cuDeviceGetUuid"); err != nil {
		return nil, err
	}

	info := &GPUInfo{Index: deviceIndex}
	for i := 0; i < 16; i++ {
		info.UUID[i] = byte(uuid[i])
	}
	return info, nil
}

// RestoreWithRemap restores VRAM with GPU remapping for migration
// oldUUID: UUID of the GPU where the checkpoint was created
// newUUID: UUID of the GPU to restore onto
func (c *Checkpointer) RestoreWithRemap(pid int, oldUUID, newUUID [16]byte) error {
	oldPtr := (*C.char)(unsafe.Pointer(&oldUUID[0]))
	newPtr := (*C.char)(unsafe.Pointer(&newUUID[0]))

	result := C.cuda_checkpoint_restore_remap(C.int(pid), oldPtr, newPtr)
	return cudaError(result, "cuCheckpointProcessRestore (remap)")
}

// MigrationPlan represents a plan for migrating a GPU process
type MigrationPlan struct {
	SourceGPU GPUInfo
	TargetGPU GPUInfo
}

// CreateMigrationPlan creates a migration plan from source to target GPU
func (c *Checkpointer) CreateMigrationPlan(sourceIndex, targetIndex int) (*MigrationPlan, error) {
	source, err := c.GetDeviceUUID(sourceIndex)
	if err != nil {
		return nil, fmt.Errorf("get source GPU: %w", err)
	}

	target, err := c.GetDeviceUUID(targetIndex)
	if err != nil {
		return nil, fmt.Errorf("get target GPU: %w", err)
	}

	return &MigrationPlan{
		SourceGPU: *source,
		TargetGPU: *target,
	}, nil
}

// RestoreWithMigration restores with GPU migration
func (c *Checkpointer) RestoreWithMigration(pid int, plan *MigrationPlan) error {
	if err := c.RestoreWithRemap(pid, plan.SourceGPU.UUID, plan.TargetGPU.UUID); err != nil {
		return err
	}
	return c.Unlock(pid)
}
