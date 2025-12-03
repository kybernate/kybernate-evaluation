# MicroK8s Setup

## Install MicroK8s

```
sudo snap install microk8s --classic --channel=latest/stable
sudo usermod -aG microk8s $USER
newgrp microk8s
```

### Install addons

#### dns

```
microk8s enable dns
```

#### hostpath-storage

```
microk8s enable hostpath-storage
```

#### dashboard

```
microk8s enable dashboard
```

##### Login

Token kopieren

```
microk8s kubectl describe secret -n kube-system microk8s-dashboard-token
microk8s kubectl get secret -n kube-system microk8s-dashboard-token \
  -o jsonpath='{.data.token}' | base64 -d; echo
```

Port forward erstellen

```
microk8s kubectl -n kubernetes-dashboard port-forward svc/kubernetes-dashboard-kong-proxy 8443:443
```

Dashboard öffnen und mit Token authentifizieren

* https://localhost:8443

##### Metrics

metrics-server should have been installed with dashboard

```
microk8s kubectl top nodes
microk8s kubectl top pods -A
```

#### ingress

```
microk8s enable ingress
```

#### registry

```
microk8s enable registry
```

### Configure Container Toolkit

Activate the nvidia addon

```
sudo tee -a /var/snap/microk8s/current/args/containerd-template.toml >/dev/null <<'EOF'

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia]
# gleicher Typ wie runc / nvidia-container-runtime
runtime_type = "${RUNTIME_TYPE}"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia.options]
BinaryName = "nvidia-container-runtime"
EOF
```

Enable the nvidia addon

```
microk8s enable nvidia --gpu-operator-driver=host --gpu-operator-set toolkit.enabled=false
```

Restart microk8s

```
sudo snap restart microk8s
```

### Install crictl

```
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

## Cleanup MicroK8s

In case we need to restart on a fresh MicroK8s installation.

Stop and remove MicroK8s

```
sudo microk8s stop
sudo snap remove microk8s
```

Cleanup old Directories

```
sudo rm -rf /var/snap/microk8s
sudo rm -rf /var/lib/microk8s
sudo rm -rf /var/lib/kubelet
sudo rm -rf /var/lib/containerd
sudo rm -rf /var/run/containerd
sudo rm -rf /var/run/microk8s

sudo rm -rf /var/snap/microk8s/common/run/containerd
sudo rm -rf /var/snap/microk8s/common/var/lib/containerd

sudo ip link delete cni0 2>/dev/null
sudo ip link delete flannel.1 2>/dev/null
sudo ip link delete kube-bridge 2>/dev/null
sudo ip link delete cali0 2>/dev/null

sudo rm -rf /etc/cni
sudo rm -rf /opt/cni
sudo rm -rf /var/snap/microk8s/common/etc/cni

rm -rf ~/.kube

sudo groupdel microk8s 2>/dev/null
```