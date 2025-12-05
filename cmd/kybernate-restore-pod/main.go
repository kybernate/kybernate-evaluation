package main

import (
"flag"
"fmt"
"os"
"path/filepath"
"text/template"
)

type RestoreConfig struct {
Name           string
Namespace      string
Image          string
CheckpointPath string
RuntimeClass   string
GPU            bool
}

const podTemplate = `apiVersion: v1
kind: Pod
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  annotations:
    kybernate.io/restore-from: "{{ .CheckpointPath }}"
spec:
  runtimeClassName: {{ .RuntimeClass }}
  restartPolicy: OnFailure
  containers:
  - name: main
    image: {{ .Image }}
    env:
    - name: RESTORE_FROM
      value: "{{ .CheckpointPath }}"
    resources:
      limits:
        {{- if .GPU }}
        nvidia.com/gpu: 1
        {{- end }}
        memory: "4Gi"
        cpu: "2"
      requests:
        {{- if .GPU }}
        nvidia.com/gpu: 1
        {{- end }}
        memory: "4Gi"
        cpu: "2"
    securityContext:
      privileged: true
`

func main() {
name := flag.String("name", "restored-pod", "Name of the restored pod")
namespace := flag.String("namespace", "default", "Namespace")
image := flag.String("image", "", "Image to use (must match original)")
checkpoint := flag.String("checkpoint", "", "Path to checkpoint directory")
runtime := flag.String("runtime", "kybernate", "RuntimeClass to use")
gpu := flag.Bool("gpu", true, "Request GPU resources")

flag.Parse()

if *checkpoint == "" || *image == "" {
fmt.Println("Error: --checkpoint and --image are required")
flag.Usage()
os.Exit(1)
}

absCheckpoint, err := filepath.Abs(*checkpoint)
if err != nil {
fmt.Printf("Error resolving checkpoint path: %v\n", err)
os.Exit(1)
}

config := RestoreConfig{
Name:           *name,
Namespace:      *namespace,
Image:          *image,
CheckpointPath: absCheckpoint,
RuntimeClass:   *runtime,
GPU:            *gpu,
}

tmpl, err := template.New("pod").Parse(podTemplate)
if err != nil {
panic(err)
}

if err := tmpl.Execute(os.Stdout, config); err != nil {
panic(err)
}
}
