# kybernate-restore-task

Restore-Variante A: kleines Node-nahes Tool, das einen containerd-Task direkt aus einem Checkpoint-Image wiederherstellt und optional CUDA-Restore ausführt.

## Build

```bash
cd /home/andre/Workspace/kybernate-evaluation
go build ./cmd/kybernate-restore-task
```

## Beispielaufruf

```bash
sudo ./kybernate-restore-task \
  -checkpoint k8s.io/pytorch-training-checkpoint:001 \
  -image docker.io/pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime \
  -id pytorch-training-restored \
  -cuda=true
```

Voraussetzung ist, dass das Checkpoint als containerd-Image vorliegt und CUDA-Checkpoint-Daten im Prozesszustand enthalten sind.
