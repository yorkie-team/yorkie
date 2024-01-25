# yorkie-mongodb

Installs MongoDB for the yorkie server, supporting both standalone mode and sharded cluster mode for storing yorkie data.

## Prerequisites

- Kubernetes 1.24+
- Helm 3+

## Get Helm Repository Info

```bash
helm repo add yorkie-team https://yorkie-team.github.io/yorkie/helm-charts
helm repo update
```

_See [`helm repo`](https://helm.sh/docs/helm/helm_repo/) for command documentation._

## Install Helm Chart

```bash
# Create mongodb namespace
kubectl create namespace mongodb

# Install yorkie monitoring helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-mongodb --namespace mongodb --set=sharded.enabled=true
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Dependencies

By default this chart installs additional, dependent charts:

- [mongodb-sharded](https://github.com/bitnami/charts/tree/main/bitnami/mongodb-sharded)

_See [`helm dependency`](https://helm.sh/docs/helm/helm_dependency/) for command documentation._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] -n mongodb
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

## Upgrading Chart

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-mongodb -n mongodb
```

With Helm v3, CRDs created by this chart are not updated by default and should be manually updated.
Consult also the [Helm Documentation on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions).

_See [`helm upgrade`](https://helm.sh/docs/helm/helm_upgrade/) for command documentation._

## Configuration

See [Customizing the Chart Before Installing](https://helm.sh/docs/intro/using_helm/#customizing-the-chart-before-installing). To see all configurable options with detailed comments:

```console
helm show values yorkie-team/yorkie-mongodb
```

You may also `helm show values` on this chart's [dependencies](#dependencies) for additional options.
