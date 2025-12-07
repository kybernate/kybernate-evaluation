#!/bin/bash
set -e

IMAGE_NAME="localhost:32000/kybernate-test-workload:latest"

echo "Building Docker image..."
docker build -t $IMAGE_NAME .

echo "Pushing image to MicroK8s registry..."
docker push $IMAGE_NAME

echo "Done! Image available at $IMAGE_NAME"
