package main

import (
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/kybernate/kybernate/pkg/service"
)

func main() {
	shim.Run("io.containerd.kybernate.v1", service.New)
}
