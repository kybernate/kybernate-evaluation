# Kybernate GPU Device Plugin Architektur

Dieses Dokument fasst den Designstand zum geplanten **Kybernate GPU Device Plugin** zusammen.  
Ziel ist es, **GPU-Sharing, GPU-Overprovisioning und Multi-GPU-Modelle** in Kubernetes effizient betreiben zu können – insbesondere in Verbindung mit Kybernate-Checkpointing (`cuda-checkpoint`) und `CRIU` für CPU-State.

---

## Hintergrund

Kubernetes behandelt `nvidia.com/gpu` als **exklusive, binäre Ressource**.  
Ein Pod kann eine GPU nur vollständig belegen – **kein Overprovisioning und keine Teilung** ist nativ vorgesehen.

Für LLM-Serving/Inference-Workloads ist das ineffizient, denn Modelle idlen häufig.  
Kybernate soll ermöglichen:

- mehrere **vLLM-Container pro physischer GPU**,
- GPU-Speicher **on-demand aktiv** laden (Restore),
- ungenutzte Modelle **checkpointen & auslagern**, um VRAM freizugeben.

Die GPU wird dadurch **nicht statisch**, sondern **aktiv verwaltet**.

---

## Warum ein eigenes Device Plugin?

Mit einem custom Device Plugin können wir definieren:

| Feature | Status |
|---|---|
| Overprovisioning (mehr Pods als GPUs) | **Ja** |
| Ressourcen flexibler als `nvidia.com/gpu` | **Ja** |
| Multi-GPU-Modelle (2, 4 GPUs pro Pod) | **Ja** |
| VRAM-Aware Scheduling ( später ) | **Ja (Kybernate-Ebene)** |
| Aktive vs. geparkte Workloads | **durch cuda-checkpoint** |
| Dynamisches Rebalancing | **Restore zwischen GPU-Gruppen** |

Wir abstrahieren GPU-Zuteilung in **Seats**, statt physische GPUs direkt zu vergeben.

---

## Kernkonzept: GPU-Groups + Seats

Wir führen zwei logische Ebenen ein:

### 1) GPU-Groups (Hardware-Einheiten)

Gruppen bündeln physische GPUs:

```

[0]       = GPU-Group G0
[1]       = GPU-Group G1
[0,1]     = GPU-Pair G01       (für Multi-GPU-Modelle)
[2,3]     = GPU-Pair G23
[0,1,2,3] = GPU-Group G0123    (für sehr große Modelle)

```

Kubernetes sieht diese Gruppen **nicht direkt**, sondern über Ressourcen.

### 2) Seats (Overprovisioned Kapazität)

Ein Seat entspricht einem möglichen **Pod-Slot auf einer GPU-Group**.

Beispiel:

| GPU-Group | GPUs enthalten | Seats | Bedeutung |
|---|---|---|---|
| G01 | 0+1 | 3 | 3 Modelle dürfen dort existieren (1 aktiv, 2 geparkt) |
| G23 | 2+3 | 3 | dito |

---

## Ressourcenmodell

Der Device Plugin exportiert **Ressourcentypen**, statt nur `nvidia.com/gpu`.

Beispiele für Ressourcentypen:

| Modellgröße | Ressource | GPUs pro Unit |
|---|---|---|
| kleine Modelle | `kyb.ai/gpu-1x-seat` | 1 GPU |
| mittlere Modelle | `kyb.ai/gpu-2x-seat` | 2 GPUs |
| große Modelle | `kyb.ai/gpu-4x-seat` | 4 GPUs |

Kubernetes-Pod benötigt dann z. B.:

```yaml
resources:
  requests:
    kyb.ai/gpu-2x-seat: 1
```

→ Device Plugin entscheidet intern, welcher GPU-Group-Seat vergeben wird.

---

## Overprovisioning und Active-Slot-Regel

Pro GPU-Group können **mehr Seats vergeben als physisch parallel aktiv sein**.

Kybernate übernimmt die Aktivierung:

```
Seats per Group = 3
Active Slots per Group = 1
```

Ablauf:

1. Pod beansprucht Seat auf Gruppe G01.
2. Kybernate entscheidet, wer **aktiv** VRAM nutzen darf.
3. Idle-Workloads werden via `cuda-checkpoint` ausgelagert.
4. Aktivierung eines anderen Seats → VRAM Restore.

Damit wird eine GPU-Group zu einem **shared but serialized Compute-Slot**.

---

## Device Plugin Responsibilities

Der Kybernate Device Plugin muss:

| Aufgabe                        | Status                              |
| ------------------------------ | ----------------------------------- |
| GPU-Topologie erkennen         | `nvidia-smi topo -m`                |
| GPU-Groups bilden              | statisch + config-getrieben         |
| Seats pro Group berechnen      | abhängig von Hardware/Config        |
| Ressourcen registrieren        | `kyb.ai/*`                          |
| Allocate()-Hook implementieren | GPU-Paths + Env-Vars + Indizierung  |
| GPU-IDs an Pods weitergeben    | `CUDA_VISIBLE_DEVICES`, `KYB_GPU_*` |

### Beispiel `Allocate()` Ergebnis

```go
resp.ContainerResponses = []*pluginapi.ContainerAllocateResponse{
  {
    Envs: map[string]string{
      "KYB_GPU_INDICES":    "0,1",
      "KYB_GPU_GROUP":      "PAIR-0-1",
      "KYB_GPU_SEAT":       "seat-2",
    },
    Devices: []*pluginapi.DeviceSpec{
      { HostPath: "/dev/nvidia0", ContainerPath: "/dev/nvidia0", Permissions: "rw" },
      { HostPath: "/dev/nvidia1", ContainerPath: "/dev/nvidia1", Permissions: "rw" },
    },
  },
}
```

Container weiß also **welche GPUs er besitzt**, und welcher Seat er ist.

---

## Node-seitiger GPU-Manager

Während der Device Plugin Seats zuteilt, übernimmt **Kybernate-GPU-Manager**:

* Tracking aller Pods pro Group
* genau **ein aktiver Seat** pro Group
* Suspend/Resume über `cuda-checkpoint/restore`
* eventuell Load-Based Shifting zwischen Gruppen (Migration)

### Beispiel: Rebalancing

```
Modelle A,B,C → G01
Modelle D,E,F → G23 (inaktiv)

Viele Requests für A–C → Kybernate migriert C → G23
```

Ablauf:

1. Checkpoint von C auf G01
2. Neuer Pod mit `kyb.ai/gpu-2x-seat` + Annotation `prefer-group: G23`
3. Device Plugin vergibt Seat auf G23
4. Restore auf GPUs 2+3 → C nun dort aktiv

---

## Flexibilität je nach Hardware/Modell

Plugin kann **dynamisch konfigurierbar** sein:

```yaml
profiles:
  - name: gpu-1x-seat
    resource: kyb.ai/gpu-1x-seat
    groupSize: 1
    seatsPerGroup: 4

  - name: gpu-2x-seat
    resource: kyb.ai/gpu-2x-seat
    groupSize: 2
    seatsPerGroup: 3

  - name: gpu-4x-seat
    resource: kyb.ai/gpu-4x-seat
    groupSize: 4
    seatsPerGroup: 2

gpuTopology:
  groups:
    - name: G01
      gpus: [0,1]
    - name: G23
      gpus: [2,3]
```

Damit können Workload-Typen unterschiedlich viel GPU-Kapazität erhalten.

---

## Zusammenfassung

* Kubernetes kann GPUs nicht nativ teilen → eigenes Device Plugin notwendig.
* **Seats** erlauben Overprovisioning mehrere Pods pro GPU-Group.
* **GPU-Groups** ermöglichen Multi-GPU-Modelle (2/4 GPUs pro Pod).
* Immer nur **1 aktives Modell pro Gruppe** → VRAM-Konflikte werden verhindert.
* Kybernate orchestriert **Suspend/Restore per cuda-checkpoint**.
* Device Plugin gibt **Resource-Typen + GPU-IDs** an Pods weiter.
* Workloads können **zwischen GPU-Groups migriert** werden.

> Ergebnis: Eine GPU kann viele Modelle hosten – aber nur aktive Modelle belegen VRAM.
> Idle-Modelle werden ausgelagert → massive Effizienzgewinne für vLLM/LLM-Serving.

---

## Nächste Schritte (Umsetzung)

1. Prototype Device Plugin mit:

   * static GPU topology
   * seat allocation
   * Allocate() mapping + EnvVars
2. Node-GPU-Manager (Active Seat Coordinator)
3. Integration mit Kybernate-Checkpoint/Restore
4. Scheduling-Hintergrundlogik (Migration / Preemption / Traffic Signals)

```

---

Wenn du möchtest, erstelle ich dir jetzt:

**A)** Code-Skeleton für das Device Plugin (Go, Kubernetes-ready)  
**B)** Beispiel DaemonSet + CRD für GPU-Manager  
**C)** Visual Diagram für die Doku  

Antwort einfach mit **A / B / C / Kombination**.
```
