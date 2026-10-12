# yorkie-cluster

Installs the yorkie-cluster, which provides cluster mode for Yorkie server to handle large amount of workloads with ensuring high availability, reliability, and scalability.

## Prerequisites

- Kubernetes 1.28+
- Helm 3+
- Istio 1.25+ (installed via Helm, see below)

### Minikube

If you are using Minikube, ensure it has enough resources allocated for the MongoDB sharded cluster:

```bash
minikube start --cpus=4 --memory=4096
```

The Percona MongoDB replsets are configured with resource requests (100m CPU, 256Mi memory per shard), so the cluster needs sufficient capacity to schedule all pods.

## Install Istio via Helm

Before installing the chart, install Istio using [Helm](https://istio.io/latest/docs/setup/install/helm/).

```bash
# Add Istio Helm repository
helm repo add istio https://istio-release.storage.googleapis.com/charts
helm repo update

# Create namespaces
kubectl create namespace istio-system
kubectl create namespace yorkie

# 1. Install Istio base (CRDs)
helm install istio-base istio/base -n istio-system \
  -f build/charts/yorkie-cluster/istio-values/base.yaml

# 2. Install istiod (control plane)
helm install istiod istio/istiod -n istio-system --wait \
  -f build/charts/yorkie-cluster/istio-values/istiod.yaml

# 3. Install yorkie-gateway
helm install yorkie-gateway istio/gateway -n yorkie --wait \
  -f build/charts/yorkie-cluster/istio-values/gateway.yaml
```

## Get Helm Repository Info

```bash
helm repo add yorkie-team https://yorkie-team.github.io/yorkie/helm-charts
helm repo update
```

_See [`helm repo`](https://helm.sh/docs/helm/helm_repo/) for command documentation._

## MongoDB Credentials (Required)

When the chart deploys MongoDB (`mongodb.enabled`, the default), it needs the
passwords of the three MongoDB users it creates:
`mongodb.credentials.databaseAdmin.password`, `userAdmin.password` and
`clusterAdmin.password`. None of them has a default, so the chart refuses to
render while any of them is blank, on Minikube as anywhere else. It also
refuses the three defaults it used to publish in its own `values.yaml` —
`admin`, `useradmin` and `clusteradmin` — since passing one of those back is
the same as having no password at all. Generate them once and keep them, as
with the signing key below, because every later `helm upgrade` has to pass the
**same** values:

```bash
# Generate once, keep them:
MONGO_DB_ADMIN_PASSWORD="$(openssl rand -base64 32)"
MONGO_USER_ADMIN_PASSWORD="$(openssl rand -base64 32)"
MONGO_CLUSTER_ADMIN_PASSWORD="$(openssl rand -base64 32)"
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKey="$(openssl rand -base64 32)" \
  --set yorkie.args.clusterSecret="$(openssl rand -base64 24)" \
  --set mongodb.credentials.databaseAdmin.password="$MONGO_DB_ADMIN_PASSWORD" \
  --set mongodb.credentials.userAdmin.password="$MONGO_USER_ADMIN_PASSWORD" \
  --set mongodb.credentials.clusterAdmin.password="$MONGO_CLUSTER_ADMIN_PASSWORD"
```

With an external MongoDB (`mongodb.enabled=false`) the chart creates no users
and needs none of these; the password goes in `yorkie.args.dbConnectionUri`
instead, which is then required — the chart has no credentials of its own to
build a URI from, and refuses to render rather than hand the server one with
an empty password.

## Admin Token Signing Key (Required)

Yorkie signs admin (dashboard and CLI) tokens with `--backend-secret-key`.
Every replica must use the same key, and anyone who knows it can act as any
user, so the chart refuses to render without one. Either keep the key in a
Secret you manage (recommended):

```bash
kubectl create secret generic yorkie-backend-secret-key --namespace yorkie \
  --from-literal=secret-key="$(openssl rand -base64 32)"
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKeyExistingSecret=yorkie-backend-secret-key \
  --set yorkie.args.clusterSecret="$(openssl rand -base64 24)"
```

or pass it as a value and let the chart create the Secret. Generate the key
once and keep it — in a values file, a secret manager, or at least a shell
variable for the session — because every later `helm upgrade` has to pass the
**same** value:

```bash
SECRET_KEY="$(openssl rand -base64 32)"   # generate once, keep it
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKey="$SECRET_KEY" \
  --set yorkie.args.clusterSecret="$(openssl rand -base64 24)"
```

Add the same setting to every `helm install` and `helm upgrade` below, including
the examples in this README: without it the chart refuses to render and the
command fails before any resource is created. The chart also rejects, at render
time, a key the server would refuse to start with — shorter than 32 characters,
or the old public default `yorkie-secret` — so generate one rather than typing a
passphrase. Both the chart and the server trim surrounding whitespace, so a
Secret made with `--from-file` works too. Changing the key invalidates every
admin token issued so far; users log in again.

The `secretKeyExistingSecret` form avoids the keep-it-around problem entirely:
the key lives in a Secret you own, and the chart never has to be told it again.
Name that Secret anything but `<yorkie.name>-credentials`, which is the chart's
own Secret (see [Where the credentials
live](#where-the-credentials-live)) — the chart rejects that name rather than
let Helm delete or refuse to adopt a Secret it half-owns. A release can move
between the two forms either way: the chart's Secret keeps the other keys and
simply stops carrying `secret-key`.

### Where the credentials live

The chart puts the signing key, the MongoDB connection URI (which carries the
database password), `yorkie.args.clusterSecret` and
`yorkie.args.githubClientSecret` in a Secret named `<yorkie.name>-credentials`,
and the Deployment reads them as the environment variables
`YORKIE_BACKEND_SECRET_KEY`, `YORKIE_MONGO_CONNECTION_URI`,
`YORKIE_CLUSTER_SECRET` and `YORKIE_AUTH_GITHUB_CLIENT_SECRET` (the key and the
cluster secret come from your own Secret instead when `secretKeyExistingSecret`
or `clusterSecretExistingSecret` is set). None of them is passed as a
command-line argument: arguments are readable in `/proc/<pid>/cmdline` and in
`kubectl get pod -o yaml`, so an argument would hand every credential to anyone
who can read a pod or get a shell in the namespace. The server also refuses the
pprof `/debug/pprof/cmdline` endpoint, which used to publish the same arguments
to anyone who could reach the profiling port this chart enables; it now answers
`404 Unknown profile`. The server reads each of those variables when the
matching flag is unset, so the same approach works outside Helm.

### Rotating the key

Rotating the key never restarts pods on its own, with either setting: the
Deployment carries no hash of the key, because a pod annotation is readable
with `get pods` — a far weaker permission than `get secrets` — and a hash of
the key is enough to search for it offline. So pods keep the old key until they
restart, and any that restart on their own pick up the new one, leaving
replicas disagreeing — which is why rotation is a deliberate two-step, never a
side effect of an unrelated upgrade. After changing the key, restart all pods
together (both names default to `yorkie`), and admin users then log in again:

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKey="$(openssl rand -base64 32)" \
  --set yorkie.args.clusterSecret="$CLUSTER_SECRET" \
  --set mongodb.credentials.databaseAdmin.password="$MONGO_DB_ADMIN_PASSWORD" \
  --set mongodb.credentials.userAdmin.password="$MONGO_USER_ADMIN_PASSWORD" \
  --set mongodb.credentials.clusterAdmin.password="$MONGO_CLUSTER_ADMIN_PASSWORD"
kubectl -n <yorkie.namespace> rollout restart deployment/<yorkie.name>
```

Only `secretKey` is new here; every other value is the one the release already
uses, re-passed because an upgrade that omits it falls back to the chart's
default. See [Upgrading Chart](#upgrading-chart) for where to read each of them
back, and use `clusterSecretExistingSecret` in place of `clusterSecret` if that
is how the release holds its cluster secret.

## Cluster RPC Secret (Required)

Nodes call each other's ClusterService through the ingress gateway
(`--backend-gateway-addr`), so the service is reachable from outside the
cluster. Without a shared secret it accepts every request, including ones that
read or purge any document. The chart therefore refuses to render unless one of
`yorkie.args.clusterSecret` and `yorkie.args.clusterSecretExistingSecret` holds
a value, as it does for the signing key. Set it either as a value or, to keep
it out of the values file, in a Secret you create once:

```bash
kubectl -n <yorkie.namespace> create secret generic <yorkie.name>-cluster-secret \
  --from-literal=cluster-secret="$(openssl rand -base64 24)"
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.clusterSecretExistingSecret=<yorkie.name>-cluster-secret \
  --set yorkie.args.secretKey="$SECRET_KEY"   # plus the other values in use
```

As with the signing key, one of the two settings has to be present on every
`helm install` and `helm upgrade`, including the examples below: without one the
chart refuses to render and the command fails before any resource is created.
The upgrade above is shortened to the setting it introduces; a real one also
re-passes every other value the release holds, as [Upgrading
Chart](#upgrading-chart) shows.

`clusterSecretExistingSecret` takes precedence over `clusterSecret`. It may name
the same Secret as `secretKeyExistingSecret`, holding both `secret-key` and
`cluster-secret`, but not `<yorkie.name>-credentials`, for the reason given
above. The chart cannot read a Secret it does not create, so it checks only
that the name is set: a Secret holding a blank `cluster-secret` passes the
render and leaves the server accepting every cluster call, with one warning in
its startup log. Check the value you stored, not just the render.

A pod with a secret refuses cluster calls that do not carry it, and a pod
without one accepts all of them. So during a rollout that turns the secret on,
calls from old pods to new ones fail; during one that changes it, calls between
old and new pods fail both ways. Either way they recover once the rollout
completes. Turning the secret on, or switching between `clusterSecret` and
`clusterSecretExistingSecret`, changes the pod template and starts a rollout by
itself. Changing the value inside the same Secret does not, as with the signing
key: run `kubectl -n <yorkie.namespace> rollout restart
deployment/<yorkie.name>` afterwards.

## Install Helm Chart

### With MongoDB (Included)

Install Yorkie cluster with MongoDB deployed by Percona Operator:

> [!IMPORTANT]
> Setting namespace is needed to ensure MongoDB is deployed in the same namespace with Yorkie cluster.

```bash
# Install yorkie cluster with MongoDB
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKey="$(openssl rand -base64 32)" \
  --set yorkie.args.clusterSecret="$(openssl rand -base64 24)" \
  --set mongodb.credentials.databaseAdmin.password="<your-password>" \
  --set mongodb.credentials.userAdmin.password="<your-password>" \
  --set mongodb.credentials.clusterAdmin.password="<your-password>"
```

### With External MongoDB

Install Yorkie cluster using an external MongoDB instance:

```bash
# Install yorkie cluster with external MongoDB
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster \
  --set yorkie.args.secretKey="$(openssl rand -base64 32)" \
  --set yorkie.args.clusterSecret="$(openssl rand -base64 24)" \
  --set yorkie.args.dbConnectionUri="mongodb://mongodb.mongodb.svc.cluster.local:27017"
```

Both examples generate the two secrets inline, which is fine for a first
install but leaves nothing to pass to the next `helm upgrade`. Keep the
generated values, or use the existing-Secret forms described above.

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] --namespace yorkie
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

Also, uninstall Istio components:

```bash
helm uninstall yorkie-gateway -n yorkie
helm uninstall istiod -n istio-system
helm uninstall istio-base -n istio-system
kubectl delete namespace istio-system
kubectl delete namespace yorkie
```

## Upgrading Chart

Pass the admin token signing key and the cluster RPC secret the release already
uses — the **same** values as the install, not freshly generated ones. A new
signing key here would leave the Secret and the running pods disagreeing until
something restarts them, one pod at a time; see [Rotating the
key](#rotating-the-key) for the deliberate path. A new cluster secret makes
calls between old and new pods fail until the rollout completes.

Pass the MongoDB passwords too, whenever the chart deploys MongoDB
(`mongodb.enabled`, the default). Like the two secrets above they have no
defaults, so an upgrade that omits them fails to render. They are written to
the `mongodb-users` Secret, which is the Percona cluster's `secrets.users`, so
pass the values the release already uses rather than new ones. Read them back
from the Secret, as below, or keep them in a values file:

```bash
# Read back the MongoDB passwords the release is using:
ns=yorkie; sec=mongodb-users
read_pw() { kubectl -n "$ns" get secret "$sec" -o jsonpath="{.data.$1}" | base64 -d; }
MONGO_DB_ADMIN_PASSWORD="$(read_pw MONGODB_DATABASE_ADMIN_PASSWORD)"
MONGO_USER_ADMIN_PASSWORD="$(read_pw MONGODB_USER_ADMIN_PASSWORD)"
MONGO_CLUSTER_ADMIN_PASSWORD="$(read_pw MONGODB_CLUSTER_ADMIN_PASSWORD)"

# Secrets kept in Secrets you own (nothing to pass but their names):
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKeyExistingSecret=yorkie-backend-secret-key \
  --set yorkie.args.clusterSecretExistingSecret=yorkie-cluster-secret \
  --set mongodb.credentials.databaseAdmin.password="$MONGO_DB_ADMIN_PASSWORD" \
  --set mongodb.credentials.userAdmin.password="$MONGO_USER_ADMIN_PASSWORD" \
  --set mongodb.credentials.clusterAdmin.password="$MONGO_CLUSTER_ADMIN_PASSWORD"

# Or held by the chart — read back the values in use:
SECRET_KEY="$(kubectl -n yorkie get secret yorkie-credentials \
  -o jsonpath='{.data.secret-key}' | base64 -d)"
CLUSTER_SECRET="$(kubectl -n yorkie get secret yorkie-credentials \
  -o jsonpath='{.data.cluster-secret}' | base64 -d)"
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set yorkie.args.secretKey="$SECRET_KEY" \
  --set yorkie.args.clusterSecret="$CLUSTER_SECRET" \
  --set mongodb.credentials.databaseAdmin.password="$MONGO_DB_ADMIN_PASSWORD" \
  --set mongodb.credentials.userAdmin.password="$MONGO_USER_ADMIN_PASSWORD" \
  --set mongodb.credentials.clusterAdmin.password="$MONGO_CLUSTER_ADMIN_PASSWORD"
```

Drop the three `mongodb.credentials.*` flags when the release points at an
external MongoDB (`mongodb.enabled=false`): the chart renders no
`mongodb-users` Secret then, and the connection URI carries the password
instead.

> [!IMPORTANT]
> **Upgrading a release installed before the admin token signing key was
> required.** The key is now mandatory, so an upgrade that passes neither
> `yorkie.args.secretKey` nor `yorkie.args.secretKeyExistingSecret` fails to
> render — including `--reuse-values` upgrades of a release that never had one.
> Pass one of the two, as in [Admin Token Signing
> Key](#admin-token-signing-key-required), on this and every later upgrade, or
> keep it in your values file. Such a release used to sign admin tokens with
> the key published in Yorkie's source, so tokens issued before the upgrade
> were forgeable by anyone; the server now refuses to start with that key, and
> every admin user logs in again after the upgrade.

> [!IMPORTANT]
> **Upgrading a release installed before the cluster RPC secret was required.**
> The secret is now mandatory too, so an upgrade that passes neither
> `yorkie.args.clusterSecret` nor `yorkie.args.clusterSecretExistingSecret`
> fails to render — again including `--reuse-values` upgrades of a release that
> never had one. Pass one of the two, as in [Cluster RPC
> Secret](#cluster-rpc-secret-required), on this and every later upgrade, or
> keep it in your values file. Such a release accepted every ClusterService
> call from outside the cluster, including ones that read or purge any
> document, so pick a fresh value rather than looking for an old one; the
> rollout it starts leaves calls between old and new pods failing until it
> completes.

> [!IMPORTANT]
> **Upgrading a release installed before the MongoDB passwords were
> required.** An upgrade that passes none of the three passwords, including a
> `--reuse-values` upgrade of a release that never set them, now fails to
> render. Read them back from `mongodb-users` as above and pass them on this and
> every later upgrade. A release that never set them still uses the defaults
> this chart used to ship — `admin`, `useradmin` and `clusteradmin`, published
> in Yorkie's source — so anyone who can reach its database can log in as the
> admin. Reading those back and passing them in fails to render too, on purpose:
> rotate instead. Change each user's password in MongoDB, pass the new values,
> then restart the Yorkie Deployment so it picks up the new connection URI.

With Helm v3, CRDs created by this chart are not updated by default and should be manually updated.
Consult also the [Helm Documentation on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions).

_See [`helm upgrade`](https://helm.sh/docs/helm/helm_upgrade/) for command documentation._

## Migrating from IstioOperator (Istio 1.17)

If you are upgrading from a previous installation that used `istioctl install`
with IstioOperator, follow the [revision-based canary upgrade](https://istio.io/latest/docs/setup/upgrade/canary/) approach:

1. Install the new Istio control plane via Helm with a revision tag:
   ```bash
   helm install istiod-1-25 istio/istiod -n istio-system \
     --set revision=1-25 \
     -f build/charts/yorkie-cluster/istio-values/istiod.yaml
   ```

2. Deploy a new gateway for the new revision:
   ```bash
   helm install yorkie-gateway-1-25 istio/gateway -n yorkie \
     --set revision=1-25 \
     -f build/charts/yorkie-cluster/istio-values/gateway.yaml
   ```

3. Switch workloads to the new revision:
   ```bash
   kubectl label namespace yorkie istio.io/rev=1-25 --overwrite
   kubectl rollout restart deployment -n yorkie
   ```

4. Verify traffic routing, consistent hashing, and ALB health checks.

5. Remove the old Istio 1.17 control plane:
   ```bash
   istioctl uninstall --revision default
   kubectl delete istiooperator -n istio-system --all
   ```

## Configuration

See [Customizing the Chart Before Installing](https://helm.sh/docs/intro/using_helm/#customizing-the-chart-before-installing). To see all configurable options with detailed comments:

```console
helm show values yorkie-team/yorkie-cluster
```

You may also `helm show values` on this chart's [dependencies](#dependencies) for additional options.
