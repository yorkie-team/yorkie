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

⚠️ **SECURITY NOTICE**: This chart requires MongoDB credentials to be provided at installation time. Passwords are NOT hardcoded for security reasons.

### For Development (Minikube):

```bash
# Uses pre-configured development credentials (clearly marked as dev-only)
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie
```

### For Production:

**Use --set flags (Recommended)**

```bash
# Generate strong random passwords
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set mongodb.credentials.databaseAdmin.password="$(openssl rand -base64 32)" \
  --set mongodb.credentials.userAdmin.password="$(openssl rand -base64 32)" \
  --set mongodb.credentials.clusterAdmin.password="$(openssl rand -base64 32)"
```

## Install Helm Chart

### With MongoDB (Included)

Install Yorkie cluster with MongoDB deployed by Percona Operator:

> [!IMPORTANT]
> Setting namespace is needed to ensure MongoDB is deployed in the same namespace with Yorkie cluster.

```bash
# Install yorkie cluster with MongoDB
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster --namespace yorkie \
  --set mongodb.credentials.databaseAdmin.password="<your-password>" \
  --set mongodb.credentials.userAdmin.password="<your-password>" \
  --set mongodb.credentials.clusterAdmin.password="<your-password>"
```

### With External MongoDB

Install Yorkie cluster using an external MongoDB instance:

```bash
# Install yorkie cluster with external MongoDB
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster \
  --set yorkie.args.dbConnectionUri="mongodb://mongodb.mongodb.svc.cluster.local:27017"
```

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

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster
```

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
