# Update Operator

A Kubernetes operator that automates OS-level updates (apt) on cluster nodes in a controlled, rolling fashion.

## How it works

The operator uses two custom resources:

- **ClusterUpdate** – defines the update schedule and controls how many nodes may be updated simultaneously (`maxUnavailableNode`).
- **NodeUpdate** – one resource per node, defines which patch image to use, the update order (`priority`), and which packages to hold back or install.

When a scheduled update cycle starts, the `ClusterUpdateController` picks the next batch of nodes (respecting `maxUnavailableNode`) and marks the corresponding `NodeUpdate` resources to trigger an update.  
The `NodeUpdateController` then creates an update pod on the respective node. The pod copies apt repository lists from the container image onto the host (via `nsenter`) and runs `apt-get upgrade`. After the pod succeeds, the node is rebooted. Once it comes back, the next batch begins.

```
ClusterUpdate (schedule, maxUnavailableNode)
      │
      ├─► NodeUpdate (node-1, priority 1)  ──► update pod ──► reboot
      ├─► NodeUpdate (node-2, priority 2)  ──► update pod ──► reboot
      └─► ...
```

## Custom Resources

### ClusterUpdate

```yaml
apiVersion: updatemanager.onesi.de/v1alpha1
kind: ClusterUpdate
metadata:
  name: default
  namespace: update
spec:
  update:
    disabled: false          # set to true to pause all updates
    schedule: "0 2 * * 0"   # cron: every Sunday at 02:00
    maxUnavailableNode: 1    # how many nodes update in parallel (default: 1)
```

### NodeUpdate

```yaml
apiVersion: updatemanager.onesi.de/v1alpha1
kind: NodeUpdate
metadata:
  name: <node-name>          # must match the Kubernetes node name
  namespace: update
spec:
  image: "ghcr.io/dusthoff/ubuntu-patch-22-04:latest"
  priority: 1                # lower = updated first
  packages:
    install: []              # explicit packages to install (empty = full dist-upgrade)
    hold:
      - kubeadm
      - kubectl
      - kubelet
      - kubernetes-cni
```

## Deploy

### Without OLM (plain kubectl)

This method works on any Kubernetes cluster without any additional tooling.

**1. Install CRDs**

```sh
kubectl apply -f deploy/plain/crds.yaml
```

**2. Create the operator namespace and RBAC**

```sh
kubectl apply -f deploy/plain/namespace.yaml
kubectl apply -f deploy/plain/rbac.yaml
```

**3. Deploy the controller**

```sh
kubectl apply -f deploy/plain/deployment.yaml
```

Verify the controller is running:

```sh
kubectl -n update-operator-system get pods
```

**4. Create a ClusterUpdate and NodeUpdate resources**

Edit `deploy/plain/clusterupdate-sample.yaml` to match your node names (run `kubectl get nodes`), then apply:

```sh
kubectl apply -f deploy/plain/clusterupdate-sample.yaml
```

**Namespace scope**

By default the controller watches all namespaces. To restrict it to a single namespace, uncomment and set the `WATCH_NAMESPACE` environment variable in `deploy/plain/deployment.yaml`:

```yaml
env:
- name: WATCH_NAMESPACE
  value: "update"
```

**Uninstall**

```sh
kubectl delete -f deploy/plain/clusterupdate-sample.yaml
kubectl delete -f deploy/plain/deployment.yaml
kubectl delete -f deploy/plain/rbac.yaml
kubectl delete -f deploy/plain/namespace.yaml
kubectl delete -f deploy/plain/crds.yaml
```

### With OLM

Requires the [Operator Lifecycle Manager](https://olm.operatorframework.io/) to be installed on the cluster.

```sh
kubectl apply -f deploy/olm/namespace.yaml
kubectl apply -f deploy/olm/catalogsource.yaml
kubectl apply -f deploy/olm/subscription.yaml
```

The OLM subscription will automatically install and manage the operator.  
After a successful install, create a `ClusterUpdate` resource:

```sh
kubectl apply -f deploy/olm/clusterupdate.yaml
```

## Development

### Prerequisites

- Go 1.21+
- [controller-gen](https://github.com/kubernetes-sigs/controller-tools) (installed automatically via `make`)
- A Kubernetes cluster (e.g. [kind](https://kind.sigs.k8s.io/))

### Run locally

```sh
# Install CRDs into the cluster
make install

# Run controller locally (uses current kubeconfig)
make run
```

### Regenerate manifests

After modifying API types, regenerate CRDs and deepcopy code:

```sh
make manifests generate
```

### Run tests

```sh
go test ./...
```

### Build and push image

```sh
make docker-build docker-push IMG=<registry>/update-operator:<tag>
```

## License

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
