# Device Plugin (GPU-Groups/Seats)

## Zweck
- Exportiert logische Ressourcen (Seats) über Kubernetes Device Plugin API, unterstützt GPU-Groups und Overprovisioning.
- Liefert Pods die benötigten GPU-Indices und Seat-Informationen; Grundlage für Active-Seat-Steuerung (Multiplexing).

## Verantwortlichkeiten
- Discovery: GPU-Topologie erfassen (nvidia-smi topo, NVML), Gruppen bilden (1x/2x/4x).
- Seats pro Gruppe berechnen (konfigurierbar) und als Ressourcen (`kyb.ai/gpu-{1x,2x,4x}-seat`) exportieren.
- Allocate(): setzt Env/Annotations: `CUDA_VISIBLE_DEVICES`, `KYB_GPU_GROUP`, `KYB_GPU_SEAT` etc.; gibt DeviceSpecs für `/dev/nvidia*` zurück.
- Health: ListAndWatch-Stream für verfügbare Seats.

## Schnittstellen
- Kubernetes Device Plugin gRPC (kubelet).
- Operator-Signale (indirekt) für Active Seat / Rebalance-Entscheidungen.
- Node-Agent/Shim nutzen die Seat/Group-Infos über Env/Annotations.

## Platzierung im Monorepo
- Code: `cmd/kyb-device-plugin/` (main) + `pkg/deviceplugin/...` (allocator, topology, nvml adapter).
- Manifeste: `manifests/device-plugin/daemonset.yaml`.
- Tests: `pkg/deviceplugin/..._test.go`.

## Implementierungshinweise
- Topology cache + periodic refresh; toleriert temporäre GPU-Ausfälle.
- Konfig: Gruppen-Profile (1x/2x/4x), seatsPerGroup, allowOverprovision=true/false.
- Allocate idempotent; bevorzugt freie Seats; kann Annot/Env für Seat ID und Group liefern.

## Security
- Läuft als DaemonSet mit Zugriff auf `/dev/nvidia*` (read), NVML libs; keine breiten HostMounts.
- Kommuniziert nur mit kubelet über gRPC UDS.

## API / Schemas (Device Plugin gRPC)
- Standard-API: `ListAndWatch`, `Allocate`, `GetDevicePluginOptions`.
- Custom Env/Annotations set by Allocate:
	- `KYB_GPU_GROUP`, `KYB_GPU_SEAT`, `CUDA_VISIBLE_DEVICES`, optional `KYB_SEAT_GEN`.

## Sequenzdiagramm (Allocation)
```
Pod admission -> kubelet -> DevicePlugin.Allocate
DevicePlugin: pick seat from group
DevicePlugin -> kubelet: DeviceSpecs (/dev/nvidiaX), Envs (group/seat/CVD)
Pod env: seat info available for Shim/Agent
```

## CRD-Beispiele
- Keine eigenen CRDs; optional ConfigMap für Gruppen/Seats-Profile:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
	name: kyb-deviceplugin-config
data:
	config.yaml: |
		profiles:
			- resource: kyb.ai/gpu-1x-seat
				seatsPerGroup: 4
			- resource: kyb.ai/gpu-2x-seat
				seatsPerGroup: 3
		groups:
			- name: G01
				gpus: [0,1]
			- name: G23
				gpus: [2,3]
```
