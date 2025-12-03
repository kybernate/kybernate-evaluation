# Kybernate Operator Design

**Status**: Proposed
**Phase**: 2
**Erstellt**: 2025-12-03

## 1. Motivation

### Warum ein Operator statt Shim?

Die ursprüngliche Idee war, Checkpoint/Restore direkt im containerd-Shim zu implementieren. Nach eingehender Evaluation haben sich jedoch folgende Limitierungen gezeigt:

| Problem | Details |
|---------|---------|
| **BinaryName-Limitation** | containerd's `BinaryName` Option für Custom Runtimes funktioniert nur mit dem Standard `runc-v2` Shim, nicht mit Custom Shims |
| **CRI API fehlt** | containerd hat die CRI `CheckpointContainer` API noch nicht implementiert ([PR #6965](https://github.com/containerd/containerd/pull/6965) offen) |
| **CRIU Mount-Bug** | Direktes `criu restore` hat bekannte Probleme mit Container-Mounts |
| **runc restore Komplexität** | Erfordert manuelles OCI-Bundle-Setup (rootfs, config.json, Namespaces) |

### Die Lösung: containerd gRPC API

containerd bietet eine **Go-basierte gRPC API**, die:
- Checkpoint/Restore intern handhabt
- Bundle-Setup automatisch erledigt
- `runc restore` korrekt aufruft

Der **Kybernate Operator** nutzt diese API direkt.

## 2. Architektur

```
┌─────────────────────────────────────────────────────────────────┐
│                      Kubernetes API Server                       │
│  ┌──────────────────┐  ┌──────────────────┐  ┌───────────────┐  │
│  │ KybernateCheckpoint│ │ KybernateRestore │  │KybernateWorkload│ │
│  │ (CRD)             │  │ (CRD)            │  │ (CRD)         │  │
│  └────────┬──────────┘  └────────┬─────────┘  └───────┬───────┘  │
└───────────┼──────────────────────┼────────────────────┼──────────┘
            │                      │                    │
            ▼                      ▼                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Kybernate Operator                           │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                    Controller Manager                       │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │ │
│  │  │ Checkpoint      │  │ Restore         │  │ Workload    │ │ │
│  │  │ Controller      │  │ Controller      │  │ Controller  │ │ │
│  │  └────────┬────────┘  └────────┬────────┘  └──────┬──────┘ │ │
│  └───────────┼────────────────────┼──────────────────┼────────┘ │
│              │                    │                  │          │
│              ▼                    ▼                  ▼          │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                  containerd Client                          │ │
│  │  - TaskService.Checkpoint()                                 │ │
│  │  - TaskService.Create(opts.WithCheckpoint())                │ │
│  │  - ContainerService.Create/Delete()                         │ │
│  └────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
            │
            ▼ (gRPC über Unix Socket)
┌─────────────────────────────────────────────────────────────────┐
│                        containerd                                │
│  /var/snap/microk8s/common/run/containerd.sock                  │
│  (oder /run/containerd/containerd.sock)                         │
│                                                                  │
│  TaskService:                                                    │
│  - Checkpoint() → CRIU checkpoint + Store as OCI Image          │
│  - Create(CheckpointPath) → Extract + runc restore              │
└─────────────────────────────────────────────────────────────────┘
```

## 3. Custom Resource Definitions (CRDs)

### 3.1 KybernateCheckpoint

```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateCheckpoint
metadata:
  name: my-gpu-checkpoint
  namespace: default
spec:
  # Ziel-Pod
  podName: gpu-inference
  containerName: model  # optional, default: erster Container
  
  # Checkpoint-Optionen
  leaveRunning: true    # Pod nach Checkpoint weiterlaufen lassen
  timeout: 300          # Sekunden, default 5 Minuten
  
  # Storage-Ziel
  storage:
    type: containerd    # containerd | registry | s3
    # Für registry:
    # image: registry.example.com/checkpoints/my-checkpoint:v1
    # Für s3:
    # bucket: kybernate-checkpoints
    # key: my-checkpoint.tar
  
  # GPU-spezifisch
  gpu:
    enabled: true       # CUDA-Plugin verwenden
    pluginPath: /usr/local/lib/criu  # CRIU Plugin-Verzeichnis

status:
  phase: Pending | InProgress | Completed | Failed
  checkpointRef: containerd.io/checkpoint/abc123:timestamp
  checkpointSize: "2.2Gi"
  startTime: "2025-12-03T12:00:00Z"
  completionTime: "2025-12-03T12:00:15Z"
  conditions:
  - type: Ready
    status: "True"
    message: "Checkpoint successfully created"
```

### 3.2 KybernateRestore

```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateRestore
metadata:
  name: my-gpu-restore
  namespace: default
spec:
  # Checkpoint-Quelle
  checkpointRef: my-gpu-checkpoint  # Referenz auf KybernateCheckpoint
  # ODER direkt:
  # checkpointImage: containerd.io/checkpoint/abc123:timestamp
  
  # Ziel-Pod Konfiguration
  targetPod:
    name: gpu-inference-restored
    # Optional: Pod-Spec überschreiben
    nodeSelector:
      kubernetes.io/hostname: gpu-node-2
    resources:
      limits:
        nvidia.com/gpu: "1"
  
  # Restore-Optionen
  timeout: 600
  
  # GPU-spezifisch  
  gpu:
    enabled: true
    # GPU muss verfügbar sein (gleiche oder kompatible)

status:
  phase: Pending | Restoring | Running | Failed
  restoredPodName: gpu-inference-restored
  restoredPodUID: xyz789
  restoreTime: "2025-12-03T12:05:00Z"
  counterValue: 45  # Für Debugging: Counter-Wert bei Restore
  conditions:
  - type: Ready
    status: "True"
    message: "Container restored and running"
```

### 3.3 KybernateWorkload (Managed Workload)

```yaml
apiVersion: kybernate.io/v1alpha1
kind: KybernateWorkload
metadata:
  name: llm-inference
  namespace: default
spec:
  # Basis Pod-Template
  template:
    spec:
      runtimeClassName: nvidia
      containers:
      - name: model
        image: my-llm:v1
        resources:
          limits:
            nvidia.com/gpu: "1"
  
  # Auto-Suspend Konfiguration
  suspend:
    enabled: true
    idleTimeout: 5m       # Suspend nach 5 Minuten Inaktivität
    storage:
      type: containerd    # Wo Checkpoint speichern
  
  # Scale-to-Zero
  scaleToZero:
    enabled: true
    minReplicas: 0
    maxReplicas: 5
  
  # Activator (Wake-on-Request)
  activator:
    enabled: true
    port: 8080
    healthPath: /health
    queueTimeout: 30s     # Max. Wartezeit während Restore

status:
  phase: Running | Suspended | Restoring
  activeReplicas: 1
  suspendedCheckpoints: 2
  lastActivityTime: "2025-12-03T12:00:00Z"
```

## 4. Controller-Implementierung

### 4.1 Checkpoint Controller

```go
// Pseudo-Code für Checkpoint Controller
func (r *CheckpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    checkpoint := &kybernatev1.KybernateCheckpoint{}
    if err := r.Get(ctx, req.NamespacedName, checkpoint); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. Pod finden
    pod := &corev1.Pod{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      checkpoint.Spec.PodName,
        Namespace: checkpoint.Namespace,
    }, pod); err != nil {
        return r.updateStatus(checkpoint, "Failed", err.Error())
    }

    // 2. Container-ID aus Pod-Status extrahieren
    containerID := getContainerID(pod, checkpoint.Spec.ContainerName)

    // 3. containerd Client erstellen
    client, err := containerd.New(r.ContainerdSocket)
    if err != nil {
        return r.updateStatus(checkpoint, "Failed", err.Error())
    }
    defer client.Close()

    // 4. Container laden
    container, err := client.LoadContainer(ctx, containerID)
    if err != nil {
        return r.updateStatus(checkpoint, "Failed", err.Error())
    }

    // 5. Task holen
    task, err := container.Task(ctx, nil)
    if err != nil {
        return r.updateStatus(checkpoint, "Failed", err.Error())
    }

    // 6. Checkpoint erstellen
    // CRIU_LIBS Environment für CUDA-Plugin setzen
    if checkpoint.Spec.GPU.Enabled {
        os.Setenv("CRIU_LIBS", checkpoint.Spec.GPU.PluginPath)
    }

    checkpointOpts := []containerd.CheckpointTaskOpts{}
    if checkpoint.Spec.LeaveRunning {
        checkpointOpts = append(checkpointOpts, containerd.WithCheckpointRuntime)
    }

    checkpointImage, err := task.Checkpoint(ctx, checkpointOpts...)
    if err != nil {
        return r.updateStatus(checkpoint, "Failed", err.Error())
    }

    // 7. Status aktualisieren
    checkpoint.Status.Phase = "Completed"
    checkpoint.Status.CheckpointRef = checkpointImage.Name()
    checkpoint.Status.CheckpointSize = formatSize(checkpointImage.Size())
    checkpoint.Status.CompletionTime = metav1.Now()

    return ctrl.Result{}, r.Status().Update(ctx, checkpoint)
}
```

### 4.2 Restore Controller

```go
// Pseudo-Code für Restore Controller
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    restore := &kybernatev1.KybernateRestore{}
    if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. Checkpoint-Image laden
    client, err := containerd.New(r.ContainerdSocket)
    if err != nil {
        return r.updateStatus(restore, "Failed", err.Error())
    }
    defer client.Close()

    checkpointImage, err := client.GetImage(ctx, restore.Spec.CheckpointImage)
    if err != nil {
        return r.updateStatus(restore, "Failed", err.Error())
    }

    // 2. Neuen Container erstellen mit Checkpoint
    container, err := client.NewContainer(ctx,
        restore.Spec.TargetPod.Name,
        containerd.WithCheckpoint(checkpointImage, restore.Spec.TargetPod.Name),
        // Weitere Optionen für GPU, Namespaces, etc.
    )
    if err != nil {
        return r.updateStatus(restore, "Failed", err.Error())
    }

    // 3. Task starten (Restore)
    task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
    if err != nil {
        return r.updateStatus(restore, "Failed", err.Error())
    }

    if err := task.Start(ctx); err != nil {
        return r.updateStatus(restore, "Failed", err.Error())
    }

    // 4. Status aktualisieren
    restore.Status.Phase = "Running"
    restore.Status.RestoredPodName = container.ID()
    restore.Status.RestoreTime = metav1.Now()

    return ctrl.Result{}, r.Status().Update(ctx, restore)
}
```

## 5. Integration mit Kubernetes

### 5.1 RBAC

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kybernate-operator
rules:
# Pod-Zugriff (readonly für Container-IDs)
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]

# Kybernate CRDs
- apiGroups: ["kybernate.io"]
  resources: ["kybernatecheckpoints", "kybernaterestores", "kybernateworkloads"]
  verbs: ["*"]

# Events für Logging
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
```

### 5.2 Operator-Deployment

```yaml
apiVersion: apps/v1
kind: DaemonSet  # DaemonSet, da wir auf containerd.sock zugreifen müssen
metadata:
  name: kybernate-operator
  namespace: kybernate-system
spec:
  selector:
    matchLabels:
      app: kybernate-operator
  template:
    metadata:
      labels:
        app: kybernate-operator
    spec:
      serviceAccountName: kybernate-operator
      hostPID: true  # Für CRIU
      containers:
      - name: operator
        image: kybernate/operator:v0.1.0
        securityContext:
          privileged: true  # Für CRIU und containerd-Zugriff
        volumeMounts:
        - name: containerd-sock
          mountPath: /run/containerd/containerd.sock
        - name: criu-plugins
          mountPath: /usr/local/lib/criu
          readOnly: true
        env:
        - name: CONTAINERD_SOCKET
          value: /run/containerd/containerd.sock
        - name: CRIU_LIBS
          value: /usr/local/lib/criu
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
      volumes:
      - name: containerd-sock
        hostPath:
          path: /var/snap/microk8s/common/run/containerd.sock  # MicroK8s
          # path: /run/containerd/containerd.sock  # Standard
      - name: criu-plugins
        hostPath:
          path: /usr/local/lib/criu
```

## 6. GPU-spezifische Überlegungen

### 6.1 RuntimeClass

GPU-Workloads müssen weiterhin `runtimeClassName: nvidia` verwenden:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: nvidia
handler: nvidia
```

Der Operator arbeitet **unabhängig** von der RuntimeClass - er interagiert direkt mit containerd.

### 6.2 CUDA-Plugin Aktivierung

```go
// Vor Checkpoint/Restore
if workload.Spec.GPU.Enabled {
    os.Setenv("CRIU_LIBS", "/usr/local/lib/criu")
}
```

### 6.3 GPU-Verfügbarkeit bei Restore

Beim Restore muss eine kompatible GPU verfügbar sein:
- Gleiche GPU-Architektur (Compute Capability)
- Gleicher oder neuerer Treiber
- Ausreichend VRAM

## 7. Nächste Schritte

### Phase 2a: Operator Skeleton
1. [ ] Kubebuilder-Projekt initialisieren
2. [ ] CRD-Schemas definieren
3. [ ] Basis-Reconciler implementieren
4. [ ] containerd-Client Integration

### Phase 2b: Checkpoint-Flow
1. [ ] Checkpoint-Controller implementieren
2. [ ] CRIU CUDA-Plugin Integration
3. [ ] E2E-Test: CPU-Workload Checkpoint

### Phase 2c: Restore-Flow
1. [ ] Restore-Controller implementieren
2. [ ] containerd `WithCheckpoint` Option testen
3. [ ] E2E-Test: CPU-Workload Restore
4. [ ] E2E-Test: GPU-Workload Restore

### Phase 2d: Managed Workloads
1. [ ] Workload-Controller implementieren
2. [ ] Idle-Detection (Prometheus Metrics)
3. [ ] Auto-Suspend/Resume

## 8. Referenzen

- [containerd Go Client](https://pkg.go.dev/github.com/containerd/containerd)
- [containerd Task API](https://github.com/containerd/containerd/blob/main/api/services/tasks/v1/tasks.proto)
- [CRIU CUDA Plugin](https://github.com/checkpoint-restore/criu/tree/master/plugins/cuda)
- [Kubebuilder Book](https://book.kubebuilder.io/)
- [KEP-2008: Forensic Container Checkpointing](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/2008-forensic-container-checkpointing)
