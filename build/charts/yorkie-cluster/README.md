# yorkie-cluster

Installs the yorkie-cluster, which provides cluster mode for Yorkie server to handle large amount of workloads with ensuring high availability, reliability, and scalability.

## Prerequisites

- Kubernetes 1.24+
- Istioctl 1.17.1+
- Helm 3+

## Install Istio with Istio Operator

Before installing the chart, you need to install Istio with [Istio Operator](https://istio.io/latest/docs/setup/install/operator/) using [istioctl](https://istio.io/latest/docs/setup/getting-started/#download).

```bash
istioctl install -f <(curl -s https://raw.githubusercontent.com/yorkie-team/yorkie/main/build/charts/yorkie-cluster/istio-operator.yaml)
```

## Get Helm Repository Info

```bash
helm repo add yorkie https://yorkie-team.github.io/yorkie/helm-charts
helm repo update
```

_See [`helm repo`](https://helm.sh/docs/helm/helm_repo/) for command documentation._

## Install Helm Chart

```bash
# Install yorkie cluster helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster -n istio-system --create-namespace
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Expose Yorkie Cluster

By default, Yorkie API is exposed via ingress with nginx ingress controller and domain `api.yorkie.dev`.
For other environments like AWS, follow the steps below:

## Expose Yorkie Cluster using AWS ALB

If you are using AWS EKS and want to expose Yorkie Cluster using AWS ALB, follow the steps below:

```bash
# Enable ALB, and change domain name and certificate arn for ALB
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster -n istio-system \
    --set externalGateway.ingressClassName=alb \
    --set externalGateway.apiHost={YOUR_API_DOMAIN_NAME} \
    --set externalGateway.adminHost={YOUR_ADMIN_DOMAIN_NAME} \
    --set externalGateway.alb.enabled=true \
    --set externalGateway.alb.certArn={YOUR_CERTIFICATE_ARN}

# Test Yorkie API
const client = new yorkie.Client('{YOUR_API_DOMAIN_NAME}');
```

Or, set configuration values in `values.yaml` file before installing the chart.

_See [configuration](#configuration) below._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] -n istio-system
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

Also, you need to uninstall istio with [istioctl](https://istio.io/latest/docs/setup/getting-started/#download).

```bash
istioctl uninstall --purge
```

This will remove all the istio components including CRDs.

## Upgrading Chart

```bash
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-cluster -n istio-system
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
