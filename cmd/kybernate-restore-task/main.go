package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/kybernate/kybernate-evaluation/pkg/cuda"
)

func main() {
	var (
		socket        = flag.String("socket", "/run/containerd/containerd.sock", "containerd socket path")
		namespace     = flag.String("namespace", "k8s.io", "containerd namespace")
		checkpointRef = flag.String("checkpoint", "", "checkpoint image reference (required)")
		imageRef      = flag.String("image", "", "original container image reference (required)")
		containerID   = flag.String("id", "", "new container ID (required)")
		withCUDA      = flag.Bool("cuda", true, "perform CUDA restore if GPU process found")
	)

	flag.Parse()

	if *checkpointRef == "" || *imageRef == "" || *containerID == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -checkpoint <ref> -image <ref> -id <id> [options]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx := namespaces.WithNamespace(context.Background(), *namespace)

	client, err := containerd.New(*socket)
	if err != nil {
		log.Fatalf("failed to connect to containerd: %v", err)
	}
	defer client.Close()

	checkpoint, err := client.GetImage(ctx, *checkpointRef)
	if err != nil {
		log.Fatalf("failed to get checkpoint image %s: %v", *checkpointRef, err)
	}

	image, err := client.GetImage(ctx, *imageRef)
	if err != nil {
		log.Fatalf("failed to get image %s: %v", *imageRef, err)
	}

	log.Printf("creating container %s from image %s", *containerID, *imageRef)

	container, err := client.NewContainer(
		ctx,
		*containerID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(*containerID+"-snap", image),
		containerd.WithNewSpec(func(s *specs.Spec) error {
			// Minimal Spec: inherit image config, defaults are fine for tests
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("failed to create container: %v", err)
	}
	defer container.Delete(ctx, containerd.WithSnapshotCleanup)

	task, err := container.NewTask(
		ctx,
		cio.NullIO,
		containerd.WithTaskCheckpoint(checkpoint),
	)
	package main

	import (
		"context"
		"flag"
		"fmt"
		"log"
		"os"
		"time"

		"github.com/containerd/containerd"
		"github.com/containerd/containerd/cio"
		"github.com/containerd/containerd/namespaces"
		"github.com/opencontainers/runtime-spec/specs-go"

		"github.com/kybernate/kybernate-evaluation/pkg/cuda"
	)

	func main() {
		var (
			socket        = flag.String("socket", "/run/containerd/containerd.sock", "containerd socket path")
			namespace     = flag.String("namespace", "k8s.io", "containerd namespace")
			checkpointRef = flag.String("checkpoint", "", "checkpoint image reference (required)")
			imageRef      = flag.String("image", "", "original container image reference (required)")
			containerID   = flag.String("id", "", "new container ID (required)")
			withCUDA      = flag.Bool("cuda", true, "perform CUDA restore if GPU process found")
		)

		flag.Parse()

		if *checkpointRef == "" || *imageRef == "" || *containerID == "" {
			fmt.Fprintf(os.Stderr, "Usage: %s -checkpoint <ref> -image <ref> -id <id> [options]\n", os.Args[0])
			flag.PrintDefaults()
			os.Exit(1)
		}

		ctx := namespaces.WithNamespace(context.Background(), *namespace)

		client, err := containerd.New(*socket)
		if err != nil {
			log.Fatalf("failed to connect to containerd: %v", err)
		}
		defer client.Close()

		checkpoint, err := client.GetImage(ctx, *checkpointRef)
		if err != nil {
			log.Fatalf("failed to get checkpoint image %s: %v", *checkpointRef, err)
		}

		image, err := client.GetImage(ctx, *imageRef)
		if err != nil {
			log.Fatalf("failed to get image %s: %v", *imageRef, err)
		}

		log.Printf("creating container %s from image %s", *containerID, *imageRef)

		container, err := client.NewContainer(
			ctx,
			*containerID,
			containerd.WithImage(image),
			containerd.WithNewSnapshot(*containerID+"-snap", image),
			containerd.WithNewSpec(func(s *specs.Spec) error {
				// Minimal Spec: inherit image config, defaults are fine for tests
				return nil
			}),
		)
		if err != nil {
			log.Fatalf("failed to create container: %v", err)
		}
		defer container.Delete(ctx, containerd.WithSnapshotCleanup)

		task, err := container.NewTask(
			ctx,
			cio.NullIO,
			containerd.WithTaskCheckpoint(checkpoint),
		)
		if err != nil {
			log.Fatalf("failed to create task from checkpoint: %v", err)
		}
		defer task.Delete(ctx)

		if err := task.Start(ctx); err != nil {
			log.Fatalf("failed to start restored task: %v", err)
		}

		pid := int(task.Pid())
		log.Printf("restored task started with pid %d", pid)

		if *withCUDA {
			if err := restoreCUDA(pid); err != nil {
				log.Printf("CUDA restore failed: %v", err)
			} else {
				log.Printf("CUDA restore successful for pid %d", pid)
			}
		}

		// Keep process running briefly so we can observe it in tests
		time.Sleep(5 * time.Second)
	}

	func restoreCUDA(pid int) error {
		ckpt, err := cuda.NewCheckpointer()
		if err != nil {
			return fmt.Errorf("init CUDA checkpointer: %w", err)
		}

		state, err := ckpt.GetState(pid)
		if err != nil {
			return fmt.Errorf("get CUDA state: %w", err)
		}

		if state != cuda.StateCheckpointed {
			return fmt.Errorf("GPU process not in checkpointed state: %s", state)
		}

		if err := ckpt.RestoreFull(pid); err != nil {
			return fmt.Errorf("RestoreFull failed: %w", err)
		}

		return nil
	}