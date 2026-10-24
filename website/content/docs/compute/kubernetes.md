---
title: Kubernetes
menu:
  main:
    parent: Compute
    weight: 20
---

> Funnel on Kubernetes is in active development and may involve frequent updates 🚧

# Quick Start

## 1. Deploying with Helm ⚡️

```sh
helm repo add ohsu https://ohsu-comp-bio.github.io/helm-charts
helm repo update
helm upgrade --install ohsu funnel
```

{{< details title="(Alternative) Deploying with `kubectl` ⚙️" >}}

### 1. Create a Service:

Deploy it:

```sh
kubectl apply -f funnel-service.yml
```

### 2. Create Funnel config files

> *[funnel-server.yaml](https://github.com/ohsu-comp-bio/funnel/blob/develop/deployments/kubernetes/funnel-server.yaml)*

> *[funnel-worker.yaml](https://github.com/ohsu-comp-bio/funnel/blob/develop/deployments/kubernetes/funnel-worker.yaml)*

Get the clusterIP:

```sh
export HOSTNAME=$(kubectl get services funnel --output=jsonpath='{.spec.clusterIP}')

sed -i "s|\${HOSTNAME}|${HOSTNAME}|g" funnel-worker.yaml
```

### 3. Create a ConfigMap

```sh
kubectl create configmap funnel-config --from-file=funnel-server.yaml --from-file=funnel-worker.yaml
```

### 4. Create a Service Account for Funnel

Define a Role and RoleBinding:

> *[role.yml](https://github.com/ohsu-comp-bio/funnel/blob/develop/deployments/kubernetes/role.yml)*

> *[role_binding.yml](https://github.com/ohsu-comp-bio/funnel/blob/develop/deployments/kubernetes/role_binding.yml)*

```sh
kubectl create serviceaccount funnel-sa --namespace default
kubectl apply -f role.yml
kubectl apply -f role_binding.yml
```

### 5. Create a Persistent Volume Claim

> *[funnel-storage-pvc.yml](https://github.com/ohsu-comp-bio/funnel/blob/develop/deployments/kubernetes/funnel-storage-pvc.yml)*

```sh
kubectl apply -f funnel-storage-pvc.yml
```

### 6. Create a Deployment

> *[funnel-deployment.yml](https://github.com/ohsu-comp-bio/funnel/blob/develop/deployments/kubernetes/funnel-deployment.yml)*

```sh
kubectl apply -f funnel-deployment.yml
```

{{< /details >}}

# 2. Proxy the Service for local testing

```sh
kubectl port-forward service/funnel 8000:8000
```

Now the funnel server can be accessed as if it were running locally. This can be verified by listing all tasks, which will return an empty JSON list:

```sh
funnel task list
# {}
```

A task can then be submitted following the [standard workflow](../../tasks):

```sh
funnel examples hello-world > hello-world.json

funnel task create hello-world.json
# <Task ID>
```

# Storage Architecture

<a href="https://www.figma.com/board/bzgv8kVL2QKESU3Sqn7S1a/Funnel-%2B-Gen3?node-id=2-1059&t=9bcuG0bMAcxBLcRD-1">
  <img title="K8s Storage" src="/img/k8s-pvc.png" />
</a>

## Per-task storage modes (`PVCMode`)

Tasks with inputs, outputs or volumes share data between the Worker and
Executor pods through a PVC. How that storage is provisioned is controlled
by `Kubernetes.PVCMode` (set the same value in the server and worker config):

| Mode | Created per task | Requirements |
|---|---|---|
| `full` (default) | PV (S3 Mountpoint CSI) + statically bound PVC | `GenericS3` `Bucket`/`Region`, S3 CSI driver, ClusterRole for `persistentvolumes` |
| `pvc` | PVC only, dynamically provisioned | `StorageClassName` of a `ReadWriteMany` StorageClass (e.g. NFS, CephFS) |
| `shared` | nothing | a pre-existing `ReadWriteMany` PVC named `SharedPVCName` (default `funnel-pvc`); tasks are isolated by `subPath` |

```yaml
Kubernetes:
  PVCMode: pvc
  StorageClassName: nfs-csi
```

`pvc` and `shared` need only namespaced RBAC, which makes them suitable for
shared/on-premise clusters without an S3 CSI driver. Custom `WorkerTemplate`,
`ExecutorTemplate` and `PVCTemplate` should reference the claim as
`{{.PVCName}}` and, in `PVCTemplate`, use `{{.StorageClassName}}` (see
`config/kubernetes/worker-pvc.yaml`). `WorkerImage` optionally overrides the
Worker Job image (default: the image of the running Funnel server pod).

The Worker derives the name of the PVC that executor pods mount from its own
`Kubernetes.PVCMode` and `SharedPVCName`, so these must match the server
config. This is automatic when the Worker config is rendered from the server
config via `ConfigMapTemplate`; with a separately maintained Worker config,
set them in both places, otherwise executor pods stay `Pending` on a missing
claim.

When Funnel does not create task-scoped ServiceAccounts (no
`ServiceAccountTemplate`, no `_WORKER_SA` tag), task and orphan cleanup do not
call the ServiceAccount API either, so a static, Helm-managed ServiceAccount
needs no `serviceaccounts` RBAC.

# Additional Resources 📚

- [Helm Repo](https://ohsu-comp-bio.github.io/helm-charts)

- [Helm Repo Source](https://github.com/ohsu-comp-bio/helm-charts)

- [Helm Charts](https://github.com/ohsu-comp-bio/funnel/tree/develop/deployments/kubernetes/helm)

- [The Chart Best Practices Guide](https://helm.sh/docs/chart_best_practices/)
