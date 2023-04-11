# yorkie-argocd

Installs the yorkie-argocd, which provides GitOps based continuous delivery for yorkie cluster.

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
# Install yorkie argocd helm chart
helm install [RELEASE_NAME] yorkie-team/yorkie-argocd -n argocd --create-namespace

# Update admin password
## bcrypt(yorkie)=$2a$12$fJoZj9CnNyD5Yfi02nZh7.XcH4Ds9M.oftQOQDP5oytyra9cP6Dny
kubectl -n argocd patch secret argocd-secret \
  -p '{"stringData": {
    "admin.password": "$2a$12$fJoZj9CnNyD5Yfi02nZh7.XcH4Ds9M.oftQOQDP5oytyra9cP6Dny",
    "admin.passwordMtime": "'$(date +%FT%T%Z)'"
}}'

# Restart argocd-server
kubectl -n argocd get pod --no-headers=true | awk '/argocd-server/{print $1}'| xargs kubectl delete -n argocd pod
```

_See [configuration](#configuration) below for custom installation_

_See [`helm install`](https://helm.sh/docs/helm/helm_install/) for command documentation._

## Expose Yorkie ArgoCD

By default, ArgoCD Web UI is exposed via ingress with nginx ingress controller and domain `api.yorkie.dev`.
For other environments like AWS, follow the steps below:

### Expose Yorkie ArgoCD using AWS ALB

If you are using AWS EKS and want to expose ArgoCD Web UI using AWS ALB, follow the steps below:

```bash
# Change externalGateway.alb.enabled to true, and certArn to your AWS certificate ARN issued in AWS Certificate Manager
helm upgrade [RELEASE_NAME] yorkie-team/yorkie-argocd -n argocd \
  --set externalGateway.ingressClassName=alb \
  --set externalGateway.apiHost={YOUR_API_DOMAIN_NAME} \
  --set externalGateway.alb.enabled=true \
  --set externalGateway.alb.certArn={YOUR_CERTIFICATE_ARN}

# Open ArgoCD Web UI
curl https://{YOUR_DOMAIN_NAME}/argocd
```

Or, set configuration values in `values.yaml` file before installing the chart.

_See [configuration](#configuration) below._

## Uninstall Helm Chart

```bash
helm uninstall [RELEASE_NAME] -n argocd
```

This removes all the Kubernetes components associated with the chart and deletes the release.

_See [`helm uninstall`](https://helm.sh/docs/helm/helm_uninstall/) for command documentation._

CRDs created by this chart are not removed by default and should be manually cleaned up:

```bash
kubectl delete crd applications.argoproj.io
kubectl delete crd applicationsets.argoproj.io
kubectl delete crd appprojects.argoproj.io
```

## Upgrading Chart

```bash
$ helm upgrade [RELEASE_NAME] yorkie-team/yorkie-argocd -n argocd
```

With Helm v3, CRDs created by this chart are not updated by default and should be manually updated.
Consult also the [Helm Documentation on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions).

_See [`helm upgrade`](https://helm.sh/docs/helm/helm_upgrade/) for command documentation._

## Configuration

See [Customizing the Chart Before Installing](https://helm.sh/docs/intro/using_helm/#customizing-the-chart-before-installing). To see all configurable options with detailed comments:

```console
helm show values yorkie-team/yorkie-argocd
```

You may also `helm show values` on this chart's [dependencies](#dependencies) for additional options.
