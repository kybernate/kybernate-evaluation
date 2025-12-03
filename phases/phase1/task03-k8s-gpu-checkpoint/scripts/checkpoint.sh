#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ARTIFACTS_DIR=${ARTIFACTS_DIR:-"$ROOT_DIR/artifacts"}
CHECKPOINT_DIR=${CHECKPOINT_DIR:-"$ARTIFACTS_DIR/checkpoints/pytorch"}
LOG_DIR=${LOG_DIR:-"$ROOT_DIR/logs"}
CRI_SOCKET=${CRI_SOCKET:-"unix:///var/snap/microk8s/common/run/containerd.sock"}
CRICTL=(sudo crictl --runtime-endpoint "$CRI_SOCKET" --image-endpoint "$CRI_SOCKET")
POD_NAME=${POD_NAME:-"pytorch-stress"}
CONTAINER_MATCH=${CONTAINER_MATCH:-"pytorch-stress"}
IGNORE_PATTERN=${IGNORE_PATTERN:-"(nvidia|etc/hosts|resolv.conf|etc/hostname)"}
CUDA_HOME=${CUDA_HOME:-"/usr/local/cuda"}
PLUGIN_DIR=${PLUGIN_DIR:-"/usr/local/lib/criu"}

echo "[checkpoint] Elevating privileges ..."
if ! sudo -v; then
  echo "Unable to obtain sudo privileges" >&2
  exit 1
fi

mkdir -p "$CHECKPOINT_DIR" "$LOG_DIR"
sudo rm -rf "$CHECKPOINT_DIR"/*

if ! command -v criu >/dev/null 2>&1; then
  echo "criu not found in PATH" >&2
  exit 1
fi

ps_output=$("${CRICTL[@]}" ps -a)
container_id=$(awk -v pat="$CONTAINER_MATCH" 'NR>1 && $0 ~ pat {print $1; exit}' <<<"$ps_output")
if [[ -z "${container_id:-}" ]]; then
  echo "No container found for pattern $CONTAINER_MATCH" >&2
  exit 1
fi

echo "Using container $container_id"

container_pid=$("${CRICTL[@]}" inspect "$container_id" | python3 -c "import json,sys; print(json.load(sys.stdin)['info']['pid'])")
if [[ -z "${container_pid:-}" ]]; then
  echo "Failed to resolve PID" >&2
  exit 1
fi

echo "Container host PID: $container_pid"

mount_file="$CHECKPOINT_DIR/mounts.txt"
sudo awk -v pattern="$IGNORE_PATTERN" '($0 ~ pattern) {printf "--external=mnt[%s]:%s\n", $1, $5}' \
  "/proc/$container_pid/mountinfo" > "$mount_file"

EXTRA_MOUNTS=()
if [[ -s "$mount_file" ]]; then
  mapfile -t EXTRA_MOUNTS < "$mount_file"
fi
MOUNT_ARGS=()
if ((${#EXTRA_MOUNTS[@]} > 0)); then
  MOUNT_ARGS=("${EXTRA_MOUNTS[@]}")
fi

export LD_LIBRARY_PATH="$CUDA_HOME/lib64:${LD_LIBRARY_PATH:-}"
log_file="$LOG_DIR/dump.log"

echo "Starting CRIU dump -> $CHECKPOINT_DIR"; echo "  (Kann bis zu 90s dauern – Fortschritt in $log_file)"
trap 'echo "CRIU dump läuft bereits – Ctrl+C wird ignoriert."' SIGINT
dump_success=0
if sudo env LD_LIBRARY_PATH="$LD_LIBRARY_PATH" criu dump \
    --tree "$container_pid" \
    --images-dir "$CHECKPOINT_DIR" \
    --tcp-established --ext-unix-sk --shell-job \
    --lib "$PLUGIN_DIR" \
    --enable-fs hugetlbfs --enable-external-masters \
    --ext-mount-map auto \
    --leave-running \
    "${MOUNT_ARGS[@]}" \
    --log-file "$log_file" -v4; then
  dump_success=1
  echo "Dump finished successfully"
else
  echo "Dump failed, check $log_file" >&2
  trap - SIGINT
  exit 1
fi
trap - SIGINT

if ((dump_success)); then
  sudo chown -R "$USER:$USER" "$CHECKPOINT_DIR" "$log_file"
fi
