// cuda-ckpt is a command-line tool for GPU checkpoint/restore using the CUDA Driver API
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kybernate/kybernate-evaluation/pkg/cuda"
)

func main() {
	var (
		action    = flag.String("action", "", "Action: lock, checkpoint, restore, unlock, state, full-checkpoint, full-restore")
		pid       = flag.Int("pid", 0, "Process ID of the CUDA process")
		timeoutMs = flag.Uint("timeout", 5000, "Lock timeout in milliseconds")
		listGPUs  = flag.Bool("list-gpus", false, "List available GPUs")
	)
	flag.Parse()

	// Initialize CUDA
	ckpt, err := cuda.NewCheckpointer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize CUDA: %v\n", err)
		os.Exit(1)
	}

	// List GPUs
	if *listGPUs {
		count, err := ckpt.GetDeviceCount()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get device count: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Found %d GPU(s):\n", count)
		for i := 0; i < count; i++ {
			info, err := ckpt.GetDeviceUUID(i)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  GPU %d: error: %v\n", i, err)
				continue
			}
			fmt.Printf("  GPU %d: %s\n", i, info.UUIDString())
		}
		return
	}

	// Require PID for checkpoint operations
	if *pid == 0 && *action != "" {
		fmt.Fprintf(os.Stderr, "Error: --pid is required\n")
		os.Exit(1)
	}

	switch *action {
	case "state":
		state, err := ckpt.GetState(*pid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get state: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(state)

	case "lock":
		if err := ckpt.Lock(*pid, *timeoutMs); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to lock: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Process locked")

	case "checkpoint":
		if err := ckpt.Checkpoint(*pid); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to checkpoint: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("VRAM checkpointed to RAM")

	case "restore":
		if err := ckpt.Restore(*pid); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to restore: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("VRAM restored from RAM")

	case "unlock":
		if err := ckpt.Unlock(*pid); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to unlock: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Process unlocked")

	case "full-checkpoint":
		fmt.Printf("Performing full checkpoint (lock + VRAM→RAM) for PID %d...\n", *pid)
		if err := ckpt.CheckpointFull(*pid, *timeoutMs); err != nil {
			fmt.Fprintf(os.Stderr, "Failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Full checkpoint complete - VRAM is now in host RAM")

	case "full-restore":
		fmt.Printf("Performing full restore (RAM→VRAM + unlock) for PID %d...\n", *pid)
		if err := ckpt.RestoreFull(*pid); err != nil {
			fmt.Fprintf(os.Stderr, "Failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Full restore complete - process is running")

	default:
		fmt.Println("cuda-ckpt - CUDA Checkpoint Tool using Driver API")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  cuda-ckpt --list-gpus                    List available GPUs")
		fmt.Println("  cuda-ckpt --action state --pid <PID>     Get checkpoint state")
		fmt.Println("  cuda-ckpt --action lock --pid <PID>      Lock process")
		fmt.Println("  cuda-ckpt --action checkpoint --pid <PID> Checkpoint VRAM to RAM")
		fmt.Println("  cuda-ckpt --action restore --pid <PID>   Restore RAM to VRAM")
		fmt.Println("  cuda-ckpt --action unlock --pid <PID>    Unlock process")
		fmt.Println("  cuda-ckpt --action full-checkpoint --pid <PID>  Lock + Checkpoint")
		fmt.Println("  cuda-ckpt --action full-restore --pid <PID>     Restore + Unlock")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --timeout <ms>  Lock timeout in milliseconds (default: 5000)")
	}
}
