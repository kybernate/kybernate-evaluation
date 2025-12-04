package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/kybernate/kybernate/pkg/cuda"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: cuda-test <pid> [checkpoint|restore]")
		os.Exit(1)
	}

	pid, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Printf("Invalid PID: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Testing CUDA checkpoint for PID %d\n", pid)

	ckpt, err := cuda.NewCheckpointer()
	if err != nil {
		fmt.Printf("Failed to create checkpointer: %v\n", err)
		os.Exit(1)
	}

	state, err := ckpt.GetState(pid)
	if err != nil {
		fmt.Printf("Failed to get state: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Current state: %s\n", state)

	if len(os.Args) > 2 {
		switch os.Args[2] {
		case "checkpoint":
			fmt.Println("Performing full checkpoint (Lock + Checkpoint)...")
			if err := ckpt.CheckpointFull(pid, 30000); err != nil {
				fmt.Printf("Checkpoint failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Checkpoint successful!")

		case "restore":
			fmt.Println("Performing full restore (Restore + Unlock)...")
			if err := ckpt.RestoreFull(pid); err != nil {
				fmt.Printf("Restore failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Restore successful!")

		default:
			fmt.Printf("Unknown action: %s\n", os.Args[2])
			os.Exit(1)
		}

		// Show new state
		state, _ = ckpt.GetState(pid)
		fmt.Printf("New state: %s\n", state)
	}
}
