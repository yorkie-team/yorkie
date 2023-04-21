# yorkie-monitoring

Installs the yorkie-monitoring, which provides monitoring system with prometheus and grafana for monitoring yorkie cluster.

## Prerequisites

- Kubernetes 1.24+
- Helm 3+

## Get Helm Repository Info

```bash
helm repo add yorkie https://yorkie-team.github.io/yorkie/helm-charts
helm repo update
```

_See [`helm repo`](https://helm.sh/docs/helm/helm_repo/) for command documentation._

## Install Helm Chart

```bash
# Install yorkie monitoring helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-monitoring -n monitoring --create-namespace

# Configure Loki datasource in Grafana
# Set Loki datasource url to http://yorkie-monitoring-loki:3100
curl https://{YOUR_API_DOMAIN_NAME}/grafana/datasources

# Import yorkie grafana dashboard and Loki grafana dashboard
curl https://grafana.com/grafana/dashboards/18451-yorkie-dashboard/
curl https://grafana.com/grafana/dashboards/13186-loki-dashboard/
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Expose Yorkie Monitoring

By default, Grafana web dashboard is exposed via ingress with nginx ingress controller and domain `api.yorkie.dev`.
For other environments like AWS, follow the steps below:

## Expose Yorkie Monitoring using AWS ALB

If you are using AWS EKS and want to expose Grafana Dashboard using AWS ALB, follow the steps below:

```bash
# Change externalGateway.alb.enabled to true, and certArn to your AWS certificate ARN issued in AWS Certificate Manager
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-monitoring -n monitoring \
  --set externalGateway.ingressClassName=alb \
  --set externalGateway.apiHost={YOUR_API_DOMAIN_NAME} \
  --set externalGateway.alb.enabled=true \
  --set externalGateway.alb.certArn={YOUR_CERTIFICATE_ARN}

# Open Grafana Dashboard
curl https://{YOUR_API_DOMAIN_NAME}/grafana
```

Or, set configuration values in `values.yaml` file before installing the chart.

_See [configuration](#configuration) below._

## Dependencies

By default this chart installs additional, dependent charts:

- [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack)

To disable dependencies during installation, see [multiple releases](#multiple-releases) below.

_See [`helm dependency`](https://helm.sh/docs/helm/helm_dependency/) for command documentation._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] -n monitoring
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

CRDs created by this chart are not removed by default and should be manually cleaned up:

```bash
kubectl get crd -oname | grep --color=never 'monitoring.coreos.com' | xargs kubectl delete
```

## Upgrading Chart

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-monitoring -n monitoring
```

With Helm v3, CRDs created by this chart are not updated by default and should be manually updated.
Consult also the [Helm Documentation on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions).

_See [`helm upgrade`](https://helm.sh/docs/helm/helm_upgrade/) for command documentation._

## Configuration

See [Customizing the Chart Before Installing](https://helm.sh/docs/intro/using_helm/#customizing-the-chart-before-installing). To see all configurable options with detailed comments:

```console
helm show values yorkie-team/yorkie-monitoring
```

You may also `helm show values` on this chart's [dependencies](#dependencies) for additional options.
