# Install all Dependencies

We are installing everything based on an Ubuntu 24.04 Noble.

## Docker

```
sudo apt-get update
sudo apt-get install ca-certificates curl -y
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}") stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update
sudo apt-get install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin -y
sudo usermod -aG docker $USER
```

## NVIDIA Driver and CUDA Toolkit 13

* https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/ubuntu.html#ubuntu-installation
* * https://developer.nvidia.com/cuda-downloads?target_os=Linux&target_arch=x86_64&Distribution=Ubuntu&target_version=24.04&target_type=deb_network

```
sudo apt remove --purge "*cuda*" "*nvidia*"
sudo apt autoremove -y

sudo apt update && sudo apt full-upgrade -y
sudo apt install -y cmake build-essential linux-headers-$(uname -r) build-essential

wget https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/x86_64/cuda-keyring_1.1-1_all.deb
sudo dpkg -i cuda-keyring_1.1-1_all.deb
sudo apt update

sudo apt install -y cuda-drivers cuda-toolkit-13-0

echo -e "blacklist nouveau\noptions nouveau modeset=0" | sudo tee /etc/modprobe.d/blacklist-nouveau.conf
sudo update-initramfs -u

sudo apt install nvtop pciutils screen curl git-lfs jq --yes

sudo reboot

nvidia-smi
```

Make cuda tool kit reachable

```
sudo ln -s /usr/local/cuda-13.0 /usr/local/cuda

sudo tee /etc/profile.d/cuda.sh >/dev/null <<EOF
export PATH=/usr/local/cuda/bin:\$PATH
export LD_LIBRARY_PATH=/usr/local/cuda/lib64:\$LD_LIBRARY_PATH
EOF

sudo chmod +x /etc/profile.d/cuda.sh

sudo reboot

which nvcc
nvcc --version
```

## Container Toolkit

```
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg && curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

sed -i -e '/experimental/ s/^#//g' /etc/apt/sources.list.d/nvidia-container-toolkit.list

sudo apt-get update

sudo apt-get install -y nvidia-container-toolkit

sudo nvidia-ctk runtime configure --runtime=docker

sudo systemctl restart docker
```

## Install CUDA checkpoint

* https://github.com/NVIDIA/cuda-checkpoint

```
cd ~
git clone https://github.com/NVIDIA/cuda-checkpoint.git
cd cuda-checkpoint
sudo cp bin/x86_64_Linux/cuda-checkpoint /usr/local/bin/
sudo chmod +x /usr/local/bin/cuda-checkpoint
cuda-checkpoint --help
```

## Install CRIU

```
sudo apt update
sudo apt install -y build-essential git pkg-config python3 protobuf-c-compiler libprotobuf-c-dev protobuf-compiler libprotobuf-dev libnl-3-dev libnl-route-3-dev libcap-dev libaio-dev libseccomp-dev libpixman-1-dev asciidoc xmlto libnftnl-dev libdrm-dev libjson-c-dev libc6-dev libbsd-dev gcc make kmod libsystemd-dev libnet1-dev libgnutls28-dev libnftables-dev python3-protobuf uuid-dev python3-yaml

cd ~
git clone https://github.com/checkpoint-restore/criu.git
cd criu

git fetch --tags
LATEST_TAG=$(git describe --tags `git rev-list --tags --max-count=1`)
git checkout $LATEST_TAG

make clean
make -j$(nproc)
sudo make install

sudo ldconfig

criu --version
sudo criu check --all
```
