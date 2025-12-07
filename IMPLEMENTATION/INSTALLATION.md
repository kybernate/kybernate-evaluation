# Installation Guide

This document replays a clean setup of the Kybernate prototype on Ubuntu 24.04 (Noble) with an NVIDIA GPU. It covers host prerequisites, GPU drivers, container tooling, MicroK8s with GPU support, and developer dependencies.

> Requires: sudo privileges and a machine with a supported NVIDIA GPU.

## 1) Base packages and Docker

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}") stable" | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker $USER
```

Log out/in (or `newgrp docker`) so the Docker group is effective.

## 2) NVIDIA driver + CUDA Toolkit 13

Clean old drivers, install current driver + toolkit, and disable nouveau:

```bash
sudo apt remove --purge "*cuda*" "*nvidia*"
sudo apt autoremove -y

sudo apt update && sudo apt full-upgrade -y
sudo apt install -y cmake build-essential linux-headers-$(uname -r)

wget https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/x86_64/cuda-keyring_1.1-1_all.deb
sudo dpkg -i cuda-keyring_1.1-1_all.deb
sudo apt update

sudo apt install -y cuda-drivers cuda-toolkit-13-0

echo -e "blacklist nouveau\noptions nouveau modeset=0" | sudo tee /etc/modprobe.d/blacklist-nouveau.conf
sudo update-initramfs -u
sudo apt install -y nvtop pciutils screen curl git-lfs jq
sudo reboot
```

After reboot verify the GPU:

```bash
nvidia-smi
```

Make the CUDA toolchain available on PATH and LD_LIBRARY_PATH:

```bash
sudo ln -s /usr/local/cuda-13.0 /usr/local/cuda

sudo tee /etc/profile.d/cuda.sh >/dev/null <<'EOF'
export PATH=/usr/local/cuda/bin:$PATH
export LD_LIBRARY_PATH=/usr/local/cuda/lib64:$LD_LIBRARY_PATH
EOF

sudo chmod +x /etc/profile.d/cuda.sh
sudo reboot
```

Validate:

```bash
which nvcc
nvcc --version
```

## 3) NVIDIA Container Toolkit

Install the runtime so containerd/Docker can launch GPU workloads:

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
  | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

sudo sed -i -e '/experimental/ s/^#//g' /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit

sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

## 4) CUDA checkpoint utility (Optional / Reference)

> **Note:** The Kybernate Node Agent uses the CUDA Driver API directly via CGO. The `cuda-checkpoint` binary is **not required** for the agent to function, but is useful for manual testing and debugging of driver capabilities.

```bash
cd ~
git clone https://github.com/NVIDIA/cuda-checkpoint.git
cd cuda-checkpoint
sudo cp bin/x86_64_Linux/cuda-checkpoint /usr/local/bin/
sudo chmod +x /usr/local/bin/cuda-checkpoint
cuda-checkpoint --help
```

## 5) CRIU (checkpoint/restore)

Build CRIU from source to match the kernel:

```bash
sudo apt update
sudo apt install -y build-essential git pkg-config python3 protobuf-c-compiler libprotobuf-c-dev protobuf-compiler \
  libprotobuf-dev libnl-3-dev libnl-route-3-dev libcap-dev libaio-dev libseccomp-dev libpixman-1-dev asciidoc xmlto \
  libnftnl-dev libdrm-dev libjson-c-dev libc6-dev libbsd-dev gcc make kmod libsystemd-dev libnet1-dev \
  libgnutls28-dev libnftables-dev python3-protobuf uuid-dev python3-yaml

cd ~
git clone https://github.com/checkpoint-restore/criu.git
cd criu

git fetch --tags
LATEST_TAG=$(git describe --tags $(git rev-list --tags --max-count=1))
git checkout $LATEST_TAG

make clean
make -j$(nproc)
sudo make install
sudo ldconfig

criu --version
sudo criu check --all
```

## 6) MicroK8s with GPU support

Install MicroK8s and core addons:

```bash
sudo snap install microk8s --classic --channel=latest/stable
sudo usermod -aG microk8s $USER
newgrp microk8s

microk8s enable dns
microk8s enable hostpath-storage
microk8s enable dashboard
microk8s enable ingress
microk8s enable registry
```

Configure containerd to expose the NVIDIA runtime and enable the GPU addon:

```bash
sudo tee -a /var/snap/microk8s/current/args/containerd-template.toml >/dev/null <<'EOF'

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia]
runtime_type = "io.containerd.runc.v2"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia.options]
BinaryName = "nvidia-container-runtime"
EOF

microk8s enable nvidia --gpu-operator-driver=host --gpu-operator-set toolkit.enabled=false
sudo snap restart microk8s
```

Optional: fetch the dashboard token and port-forward for UI access.

### Install crictl pinned to the MicroK8s version

```bash
echo "👉 Fetching Kubernetes server version from MicroK8s ..."

# 1. Get Kubernetes version from MicroK8s (Server Version)
# Example output: 'Server Version: v1.30.2'
K8S_VERSION_FULL=$(microk8s kubectl version | awk '/Server Version/ {print $3}')

if [[ -z "${K8S_VERSION_FULL:-}" ]]; then
  echo "❌ Could not determine Kubernetes version. Is microk8s running?"
  exit 1
fi

# 'v1.30.2' -> '1.30.2'
K8S_VERSION_FULL=${K8S_VERSION_FULL#v}

# Use only Major.Minor -> '1.30'
K8S_MAJOR_MINOR=$(echo "$K8S_VERSION_FULL" | cut -d. -f1-2)

echo "✅ Found Kubernetes version (Server): $K8S_VERSION_FULL (Major.Minor: $K8S_MAJOR_MINOR)"

# 2. Derive matching crictl version (e.g., v1.30.0)
CRICTL_VERSION="v${K8S_MAJOR_MINOR}.0"
echo "👉 Using crictl version: $CRICTL_VERSION"

# 3. Determine architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)
    CRICTL_ARCH="amd64"
    ;;
  aarch64|arm64)
    CRICTL_ARCH="arm64"
    ;;
  *)
    echo "❌ Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

echo "✅ Architecture: $ARCH (crictl Arch: $CRICTL_ARCH)"

# 4. Download crictl from GitHub
CRICTL_URL="https://github.com/kubernetes-sigs/cri-tools/releases/download/${CRICTL_VERSION}/crictl-${CRICTL_VERSION}-linux-${CRICTL_ARCH}.tar.gz"

echo "👉 Downloading crictl from: $CRICTL_URL"

TMP_TAR="/tmp/crictl-${CRICTL_VERSION}.tar.gz"
curl -L "$CRICTL_URL" -o "$TMP_TAR"

echo "✅ Download complete: $TMP_TAR"

# 5. Extract to /usr/local/bin
echo "👉 Installing crictl to /usr/local/bin ..."
sudo mkdir -p /usr/local/bin
sudo tar zxvf "$TMP_TAR" -C /usr/local/bin

# Cleanup
rm -f "$TMP_TAR"

# 6. Write crictl config for MicroK8s containerd
echo "👉 Writing /etc/crictl.yaml for MicroK8s ..."

# MicroK8s containerd socket
CONTAINERD_SOCKET="unix:///var/snap/microk8s/common/run/containerd.sock"

sudo tee /etc/crictl.yaml >/dev/null <<EOF
runtime-endpoint: ${CONTAINERD_SOCKET}
image-endpoint: ${CONTAINERD_SOCKET}
EOF
```

### Resetting MicroK8s (if needed)

```bash
sudo microk8s stop
sudo snap remove microk8s
sudo rm -rf /var/snap/microk8s /var/lib/microk8s /var/lib/kubelet /var/lib/containerd \
  /var/run/containerd /var/run/microk8s /var/snap/microk8s/common/run/containerd \
  /var/snap/microk8s/common/var/lib/containerd /etc/cni /opt/cni
sudo ip link delete cni0 2>/dev/null
sudo ip link delete flannel.1 2>/dev/null
sudo ip link delete kube-bridge 2>/dev/null
sudo ip link delete cali0 2>/dev/null
rm -rf ~/.kube
sudo groupdel microk8s 2>/dev/null
```

## 7) Developer toolchain

Install a modern Go toolchain and Kubernetes build utilities.

```bash
sudo add-apt-repository ppa:longsleep/golang-backports
sudo apt update
sudo apt install -y golang-go

echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc
source ~/.bashrc

go version
```

Protocol Buffers and gRPC plugins for Go:

```bash
sudo apt install -y protobuf-compiler
protoc --version

go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

General build helpers:

```bash
sudo apt install -y make git build-essential
```

Optional Kubernetes dev tools:

```bash
curl -L -o kubebuilder https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)
chmod +x kubebuilder && sudo mv kubebuilder /usr/local/bin/

curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash
sudo mv kustomize /usr/local/bin/
```

After installation, log out/in (or `newgrp microk8s` and `newgrp docker`) to ensure group memberships and environment changes are active.
