# MicroK8s Setup

## First initial tests

```
microk8s status --wait-ready
microk8s kubectl version
microk8s kubectl get nodes
microk8s kubectl get pods -A
microk8s kubectl describe node | grep -A 6 "Capacity:"
microk8s kubectl get nodes -o json | jq '.items[].status.allocatable'
```

Create a pod and delete it afterwards

```
microk8s kubectl run test --image=nginx --restart=Never
microk8s kubectl get pod test -w
microk8s kubectl delete pod test
```

### Test addons

#### dns

```
microk8s kubectl run dns-test --image=busybox:1.36 --command -- sleep 3600
microk8s kubectl exec dns-test -- nslookup kubernetes.default.svc.cluster.local
microk8s kubectl delete pod dns-test
microk8s kubectl get svc kubernetes
microk8s kubectl get svc kubernetes -n default
microk8s kubectl get pods -n kube-system -o wide | grep -i coredns
microk8s kubectl get svc -n kube-system | grep -E 'dns|kube-dns'
```

#### hostpath-storage

```
microk8s kubectl get storageclass
microk8s kubectl get storageclass | grep "(default)"
microk8s kubectl get pods -n kube-system | grep hostpath
```

Create a pod, that uses the storage

```
cat <<EOF | microk8s kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: test-pvc-pod
spec:
  containers:
  - name: app
    image: busybox
    command: ["/bin/sh", "-c", "echo OK > /data/out; sleep 3600"]
    volumeMounts:
    - mountPath: /data
      name: vol
  volumes:
  - name: vol
    persistentVolumeClaim:
      claimName: test-pvc
EOF
```

##### Test the pod

```
microk8s kubectl exec test-pvc-pod -- cat /data/out
microk8s kubectl get pvc test-pvc
microk8s kubectl describe pvc test-pvc
microk8s kubectl get pod test-pvc-pod -o wide
microk8s kubectl describe pod test-pvc-pod
microk8s kubectl get pv
microk8s kubectl describe pv <NAME_AUS_PVC>
microk8s kubectl delete pod test-pvc-pod
microk8s kubectl delete pvc test-pvc
```

#### dashboard

```
microk8s kubectl get pods -n kubernetes-dashboard
microk8s kubectl get svc -n kubernetes-dashboard
```

#### ingress

Create a Deployment

```
microk8s kubectl create deployment hello --image=nginx
microk8s kubectl expose deployment hello --port=80
```

Create the ingress

```
cat <<EOF | microk8s kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hello-ingress
spec:
  rules:
  - host: hello.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: hello
            port:
              number: 80
EOF
```

Curl the ingress

```
curl --resolve hello.local:80:127.0.0.1 http://hello.local
```

Cleanup

```
microk8s kubectl delete ingress hello-ingress
microk8s kubectl delete service hello
microk8s kubectl delete deployment hello
microk8s kubectl get ingress
microk8s kubectl get svc
microk8s kubectl get deploy
```

#### registry

Show the registry resources

```
microk8s kubectl get pvc -n container-registry
microk8s kubectl get svc -n container-registry
microk8s kubectl describe svc registry -n container-registry
microk8s kubectl get deployment -n container-registry
microk8s kubectl describe deployment registry -n container-registry
microk8s kubectl get pod -n container-registry
microk8s kubectl describe pod -n container-registry <registry-pod-name>
```

Create an image and push to the registry

```
echo -e "FROM busybox\nCMD echo OK" > Dockerfile
docker build -t localhost:32000/testimage:1 .
docker push localhost:32000/testimage:1
```

Create a pod

```
microk8s kubectl run regtest --image=localhost:32000/testimage:1 --restart=Never
microk8s kubectl logs regtest
```

List the image

```
curl -s http://localhost:32000/v2/_catalog
curl -s http://localhost:32000/v2/testimage/tags/list
```

Cleanup

```
microk8s kubectl delete pod regtest
microk8s disable registry
microk8s kubectl delete pvc --all -n container-registry
microk8s enable registry
docker rmi -f localhost:32000/testimage:1
```

#### nvidia

Get the runtimeclass

```
microk8s kubectl get runtimeclass
```

Create a pod and assign one GPU

```
microk8s kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test
spec:
  restartPolicy: Never
  runtimeClassName: nvidia    # WICHTIG: Explizit die NVIDIA Runtime anfordern
  containers:
    - name: cuda-container
      # WICHTIG: Das 'runtime'-Image verwenden, nicht 'base', damit nvidia-smi drin ist
      image: nvidia/cuda:12.3.1-runtime-ubuntu22.04
      command: ["nvidia-smi"]
      resources:
        limits:
          nvidia.com/gpu: 1
EOF
```

Describe the pod and fetch the logs

```
microk8s kubectl describe pod gpu-test
microk8s kubectl logs gpu-test
```

Delete the pod

```
microk8s kubectl delete pod gpu-test
```

### crictl

```
echo "✅ /etc/crictl.yaml created:"
cat /etc/crictl.yaml

# 7. Functionality test
echo "👉 Testing crictl ..."
crictl version || { echo '❌ crictl version failed'; exit 1; }

echo "👉 Listing running containers (if any) ..."
crictl ps || { echo '⚠️ crictl ps reported an error – is MicroK8s running and are there pods?'; }

echo "🎉 crictl is installed and configured with MicroK8s."
```
