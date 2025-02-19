# yorkie-analytics

Installs the yorkie-analytics, which provides analytics system with StarRocks and Grafana for analyizing usage of yorkie cluster.

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
# Install yorkie analytics helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-analytics -n analytics --create-namespace
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Dependencies

By default this chart installs additional, dependent charts:

- [kube-starrocks](https://starrocks.github.io/starrocks-kubernetes-operator)
- [bitnami/kafka](https://github.com/bitnami/charts/tree/main/bitnami/kafka)

_See [`helm dependency`](https://helm.sh/docs/helm/helm_dependency/) for command documentation._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] -n analytics
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

## Upgrading Chart

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-analytics -n analytics
```

With Helm v3, CRDs created by this chart are not updated by default and should be manually updated.
Consult also the [Helm Documentation on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions).

_See [`helm upgrade`](https://helm.sh/docs/helm/helm_upgrade/) for command documentation._

## Configuration

See [Customizing the Chart Before Installing](#customizing-the-chart-before-installing) below for configuration options. To see all configurable options with detailed comments:

```console
helm show values yorkie-team/yorkie-analytics
```

You may also `helm show values` on this chart's [dependencies](#dependencies) for additional options.
