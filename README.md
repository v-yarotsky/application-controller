# Application Controller

Application Controller provides a simplified way of deploying and _updating_ single-container applications to
a homelab Kubernetes cluster.

## Description

Application controller automatically manages the following Kubernetes resources for an Application:
- Deployment
- ServiceAccount
- Service
- Ingress
- PodMonitor
- RoleBinding
- ClusterRoleBinding

It automatically updates the Deployment whenever a new version of the container image becomes available.

Example resource:

```yaml
apiVersion: yarotsky.me/v1alpha1
kind: Application
metadata:
  name: application-sample
spec:
  image:
    repository: "git.home.yarotsky.me/vlad/dashboard"
    versionStrategy: "SemVer"
    semver:
      constraint: "^0"
  ports:
  - name: "http"
    containerPort: 8080
  - name: "metrics"
    containerPort: 8192
  ingress:
    host: "dashboard.home.yarotsky.me"
  metrics:
    enabled: true
```

Expanded example:

```yaml
apiVersion: yarotsky.me/v1alpha1
kind: Application
metadata:
  name: application-sample
spec:
  image:
    repository: "git.home.yarotsky.me/vlad/dashboard"
    versionStrategy: "SemVer"
    semver:
      constraint: "^0"
    updateSchedule: "@every 5m"  # see https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format;
                                 # default can be set via `--default-update-schedule`
  command: ["/bin/dashboard"]
  args: ["start-server"]
  env:
  - name: "FOO"
    value: "bar"
  - name: "QUX"
    valueFrom:
      secretKeyRef:
        name: "my-secret"
        key: "my-secret-key"
  ports:
  - name: "http"
    containerPort: 8080
  - name: "metrics"
    containerPort: 8192
  - name: "listen-udp"
    protocol: "UDP"
    containerPort: 22000
  ingress:
    # Ingress annotations can be set via `--ingress-annotations`.
    host: "dashboard.home.yarotsky.me"
    ingressClassName: "nginx-private"  # default can be set via `--ingress-class`
    portName: "http"                   # defaults to `"web" `or `"http" `if present in `.spec.ports`
  loadBalancer:
    host: "udp.home.yarotsky.me"
    portNames: ["listen-udp"]
  metrics:
    enabled: true
    portName: "metrics"  # defaults to `"metrics"` or `"prometheus" `if present in `.spec.ports`
    path: "/metrics"     # defaults to `"/metrics"`
  resources:
    requests:
      cpu: "100m"
    limits:
      cpu: "250m"
  probe:
    httpGet:
      path: "/healthz"
  securityContext:
    runAsUser: 1000
    runAsGroup: 1000
  volumes:
  - name: "my-volume"
    volumeSource:
      persistentVolumeClaim:
        claimName: "my-pvc"
    mountPath: "/data"
  roles:
  - apiGroup: "rbac.authorization.k8s.io"
    kind: "ClusterRole"
    name: "my-cluster-role"
    scope: "Cluster"
  - apiGroup: "rbac.authorization.k8s.io"
    kind: "ClusterRole"
    name: "my-cluster-role2"
    scope: "Namespace"
  - apiGroup: "rbac.authorization.k8s.io"
    kind: "Role"
    name: "my-role"
```

## Container Image Registry Authentication


## Monitoring

See https://book.kubebuilder.io/reference/metrics-reference.

In addition to the above metrics, the following metrics are instrumented:

| Name                         | Description                                   | Tags                                          |
| ---                          | ---                                           | ---                                           |
| `image_registry_calls_total` | Number of calls to a Container Image Registry | `registry`, `repository`, `success`, `method` |


# TODO

- [X] Automatically update docker images of Applications (digest).
- [X] Unhardcode reference to image pull secret (accept many via configuration)
- [X] Allow configuration of default Ingress annotations via controller config.
- [X] Gracefully manage updates
  - [X] To Container/Service/Ingress configurations
  - [X] To Roles
- [X] Expose meaningful status on the `Application` CR.
- [X] Expose Events on the `Application` CR (e.g. when we're updating the image).
- [X] Expose current image in status
- [X] Garbage-collect `ClusterRoleBinding` objects (they cannot be auto-removed via ownership relationship).
- [X] Automatically update docker images of Applications (semver).
- [X] Add `app` short name
- [X] Ensure we don't hammer the image registry on errors (requeue reconciliation with increased interval) - solved via caching image refs
- [X] Support different update schedules
  - [X] Allow Applications to pick a particular update schedule
  - [X] Allow choosing a default one
- [X] Update README
- [X] Add prometheus metrics
- [X] Prune Ingress, Service (LoadBalancer) when `ingress` or `loadBalancer` are removed.
- [X] Allow specifying a mixture of container SecurityContext and PodSecurityContext (rather, allow setting `fsGroup` on pod template level)
- [ ] Validating admission webhook? Or at least write tests to make sure we have nice error messages

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Running on the cluster
1. Install Instances of Custom Resources:

```sh
kubectl apply -k config/samples/
```

2. Build and push your image to the location specified by `IMG`:

```sh
make docker-build docker-push IMG=<some-registry>/application-controller:tag
```

3. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/application-controller:tag
```

### Uninstall CRDs
To delete the CRDs from the cluster:

```sh
make uninstall
```

### Undeploy controller
UnDeploy the controller from the cluster:

```sh
make undeploy
```

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/),
which provide a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster.

### Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

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
