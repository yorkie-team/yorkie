{{/*
Name of the Secret that holds the inter-node cluster secret. Operators can
point yorkie.args.clusterSecretExistingSecret at a Secret they manage; the
chart then creates none of its own.
*/}}
{{- define "yorkie.clusterSecretName" -}}
{{- if .Values.yorkie.args.clusterSecretExistingSecret -}}
{{ .Values.yorkie.args.clusterSecretExistingSecret }}
{{- else -}}
{{ .Values.yorkie.name }}-cluster-secret
{{- end -}}
{{- end -}}

{{/*
The inter-node cluster secret itself.

This chart runs several replicas and the server fails inter-node RPCs closed,
so an empty value cannot be passed through: each replica would fall back to a
secret it generated for itself and none of them could call the others. The
order is therefore explicit value, then the value already stored in the Secret
(so upgrades keep the running cluster's secret), then a fresh random one.

`lookup` returns nothing when there is no cluster to read - `helm template`,
`--dry-run`, GitOps diff - so those renders mint a new value every time. That
is why values.yaml asks such pipelines to set clusterSecret explicitly.
*/}}
{{- define "yorkie.clusterSecret" -}}
{{- if .Values.yorkie.args.clusterSecret -}}
{{ .Values.yorkie.args.clusterSecret }}
{{- else -}}
{{- $name := printf "%s-cluster-secret" .Values.yorkie.name -}}
{{- $key := .Values.yorkie.args.clusterSecretKey -}}
{{- $existing := lookup "v1" "Secret" .Values.yorkie.namespace $name -}}
{{- if $existing -}}
{{- $stored := index $existing.data $key -}}
{{- if $stored -}}
{{- $stored | b64dec -}}
{{- else -}}
{{- randAlphaNum 32 -}}
{{- end -}}
{{- else -}}
{{- randAlphaNum 32 -}}
{{- end -}}
{{- end -}}
{{- end -}}
