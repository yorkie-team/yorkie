# yorkie-cluster

Installs the yorkie-cluster, which provides cluster mode for Yorkie server to handle large amount of workloads with ensuring high availability, reliability, and scalability.

## Prerequisites

- Kubernetes 1.23+
- Istioctl 1.17+
- Helm 3+

## Install Istio with Istio Operator

Before installing the chart, you need to install Istio with [Istio Operator](https://istio.io/latest/docs/setup/install/operator/) using [istioctl](https://istio.io/latest/docs/setup/getting-started/#download).

```bash
kubectl create namespace yorkie
istioctl install -f <(curl -s https://raw.githubusercontent.com/yorkie-team/yorkie/main/build/charts/yorkie-cluster/istio-operator.yaml)
```

## Get Helm Repository Info

```bash
helm repo add yorkie-team https://yorkie-team.github.io/yorkie/helm-charts
helm repo update
```

_See [`helm repo`](https://helm.sh/docs/helm/helm_repo/) for command documentation._

## Install Helm Chart

```bash
# Install yorkie cluster helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME]
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

Also, you need to uninstall istio with [istioctl](https://istio.io/latest/docs/setup/getting-started/#download).

```bash
istioctl uninstall --purge
kubectl delete namespace yorkie
```

This will remove all the istio components including CRDs.

## Upgrading Chart

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster
```

With Helm v3, CRDs created by this chart are not updated by default and should be manually updated.
Consult also the [Helm Documentation on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions).

_See [`helm upgrade`](https://helm.sh/docs/helm/helm_upgrade/) for command documentation._

## Configuration

See [Customizing the Chart Before Installing](https://helm.sh/docs/intro/using_helm/#customizing-the-chart-before-installing). To see all configurable options with detailed comments:

```console
helm show values yorkie-team/yorkie-cluster
```

You may also `helm show values` on this chart's [dependencies](#dependencies) for additional options.
