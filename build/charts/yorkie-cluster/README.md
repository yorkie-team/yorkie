# yorkie-cluster

Installs the yorkie-cluster, which provides cluster mode for Yorkie server to handle large amount of workloads with ensuring high availability, reliability, and scalability.

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
# Install yorkie cluster helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-cluster -n istio-system --create-namespace

# Redeploy istio ingress gateway with auto injecton
kubectl rollout restart deployment istio-ingressgateway -n istio-system
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Expose Yorkie Cluster

By default, Yorkie API is exposed via ingress with nginx ingress controller and domain `api.yorkie.dev`.
For other environments like AWS, follow the steps below:

## Expose Yorkie Cluster using AWS ALB

If you are using AWS EKS and want to expose Yorkie Cluster using AWS ALB, follow the steps below:

```bash
# Change istio-ingressgateway service type to NodePort, externalGateway.alb.enabled to true, and certArn to your AWS certificate ARN issued in AWS Certificate Manager
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

## Dependencies

By default this chart installs additional, dependent charts:

- [base](https://github.com/istio/istio/tree/master/manifests/charts/base)
- [istiod](https://github.com/istio/istio/tree/master/manifests/charts/istio-control/istio-discovery)
- [gateway](https://github.com/istio/istio/tree/master/manifests/charts/gateway)

To disable dependencies during installation, see [multiple releases](#multiple-releases) below.

_See [`helm dependency`](https://helm.sh/docs/helm/helm_dependency/) for command documentation._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] -n istio-system
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

CRDs created by this chart are not removed by default and should be manually cleaned up:

```bash
kubectl delete crd authorizationpolicies.security.istio.io
kubectl delete crd destinationrules.networking.istio.io
kubectl delete crd envoyfilters.networking.istio.io
kubectl delete crd gateways.networking.istio.io
kubectl delete crd istiooperators.install.istio.io
kubectl delete crd peerauthentications.security.istio.io
kubectl delete crd proxyconfigs.networking.istio.io
kubectl delete crd requestauthentications.security.istio.io
kubectl delete crd serviceentries.networking.istio.io
kubectl delete crd sidecars.networking.istio.io
kubectl delete crd telemetries.telemetry.istio.io
kubectl delete crd virtualservices.networking.istio.io
kubectl delete crd wasmplugins.extensions.istio.io
kubectl delete crd workloadentries.networking.istio.io
kubectl delete crd workloadgroups.networking.istio.io
```

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
