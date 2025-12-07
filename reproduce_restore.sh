#!/bin/bash
set -e

echo "Cleaning up previous test..."
sudo rm -rf /tmp/restore-test
mkdir -p /tmp/restore-test/rootfs

echo "Exporting rootfs..."
sudo docker rm -f temp-rootfs || true
sudo docker run -d --name temp-rootfs localhost:32000/gpu-pytorch:v1 sleep 100
sudo docker export temp-rootfs | sudo tar -x -C /tmp/restore-test/rootfs
sudo chmod 755 /tmp/restore-test/rootfs
sudo docker rm -f temp-rootfs

echo "Preparing bundle..."
sudo cp /var/snap/microk8s/common/run/debug-config.json /tmp/restore-test/config.json
sudo cp -r /tmp/kybernate-checkpoint /tmp/restore-test/checkpoint

echo "Adjusting config.json..."
# We need to remove some namespaces or adjust paths if they are specific to the pod
# But let's try as is first.
# Note: The config.json has "rootfs": "rootfs", which is correct relative to /tmp/restore-test.

echo "Running runc restore..."
cd /tmp/restore-test
# We use the system runc. MicroK8s uses its own runc, but system runc should be compatible for this test.
# Or we can use /var/snap/microk8s/common/run/runc if available?
# Usually it's runc.
sudo runc --version

# Try restore
sudo runc restore --image-path checkpoint --work-path checkpoint --bundle . test-restore
