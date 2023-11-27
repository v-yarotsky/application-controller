# Application Controller

Application Controller provides a simplified way of deploying and _updating_ single-container applications to
a homelab Kubernetes cluster.

## Description

Application controller automatically manages the following Kubernetes resources for an Application:
- Deployment
- ServiceAccount
- Service
- IngressRoute
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
    portName: "http"                   # defaults to `"web" `or `"http" `if present in `.spec.ports`
    auth:
      enabled: true  # Enables authentication proxy for this IngressRoute.
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

## Prerequisites

```yaml
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    providers:
      kubernetesCRD:
        allowCrossNamespace: true
      service:
        annotations:
          "external-dns.alpha.kubernetes.io/hostname": "my.traefik.ingress.example.com"
```

## Auth proxy

### Additional prerequisites

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: Middleware
metadata:
  name: oauth2-signin
  namespace: kube-system
spec:
  errors:
    query: /oauth2/sign_in
    service:
      name: oauth2-proxy
      namespace: kube-system
      port: http
    status:
    - "401"
```

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: Middleware
metadata:
  name: oauth2-forward
  namespace: kube-system
spec:
  forwardAuth:
    address: https://auth.home.yarotsky.me/oauth2/auth
    trustForwardHeader: true
```

```yaml
apiVersion: traefik.containo.us/v1alpha1
kind: Middleware
metadata:
  name: forward-auth
  namespace: kube-system
spec:
  chain:
    middlewares:
    - name: oauth2-signin
      namespace: kube-system
    - name: oauth2-forward
      namespace: kube-system
```

Next, install [OAuth2 Proxy](https://github.com/oauth2-proxy/oauth2-proxy), with the following configuration:

```yaml
OAUTH2_PROXY_HTTP_ADDRESS: "0.0.0.0:4180",
OAUTH2_PROXY_COOKIE_DOMAINS: ".example.com",
OAUTH2_PROXY_WHITELIST_DOMAINS: ".example.com",
OAUTH2_PROXY_PROVIDER: "oidc",
OAUTH2_PROXY_CLIENT_ID: "oauth2-proxy",
OAUTH2_PROXY_CLIENT_SECRET: "<OIDC provider client secret>"
OAUTH2_PROXY_EMAIL_DOMAINS: "*",
OAUTH2_PROXY_OIDC_ISSUER_URL: oidcClient.discoveryURL,
OAUTH2_PROXY_REDIRECT_URL: redirectUrl,
OAUTH2_PROXY_COOKIE_CSRF_PER_REQUEST: "true",
OAUTH2_PROXY_COOKIE_CSRF_EXPIRE: '5m',
OAUTH2_PROXY_REVERSE_PROXY: "true",
OAUTH2_PROXY_SET_XAUTHREQUEST: "true",
// Needed to disable the mandatory validated email requirement, if you know what you're doing
// Ref: https://joeeey.com/blog/selfhosting-sso-with-traefik-oauth2-proxy-part-2#https://joeeey.com/blog/selfhosting-sso-with-traefik-oauth2-proxy-part-2/
OAUTH2_PROXY_INSECURE_OIDC_ALLOW_UNVERIFIED_EMAIL: "true",
OAUTH2_PROXY_OIDC_EMAIL_CLAIM: "sub",
// Needed for multiple subdomains support
// Ref: https://github.com/oauth2-proxy/oauth2-proxy/issues/1297#issuecomment-1564124675
OAUTH2_PROXY_FOOTER: "<script>(function(){var rd=document.getElementsByName('rd');for(var i=0;i<rd.length;i++)rd[i].value=window.location.toString().split('/oauth2')[0]})()</script>"
OAUTH2_PROXY_COOKIE_SECRET: "<32-byte cookie secret>"
```

The following flags should be supplied to the application controller:

```
--traefik-cname-target=my.traefik.ingress.example.com \
--traefik-auth-path-prefix=/oauth2/ \
--traefik-auth-service-name=kube-system/oauth2-proxy \
--traefik-auth-service-port-name=http \
--traefik-auth-middleware-name=kube-system/forward-auth \
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
