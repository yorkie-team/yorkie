{{- define "configReplName" -}}
{{- $name := index . 0 -}}
{{- printf "%s-configsvr" $name -}}
{{- end -}}

{{- define "configReplAddr" -}}
{{- $name := index . 0 -}}
{{- $replIndex := index . 1 -}}
{{- $domainSuffix := index . 2 -}}
{{- printf "%s-configsvr-%d.%s-headless.%s" $name $replIndex $name $domainSuffix -}}
{{- end -}}

{{- define "shardReplName" -}}
{{- $name := index . 0 -}}
{{- $index := index . 1 -}}
{{- printf "%s-shard-%d" $name $index -}}
{{- end -}}

{{- define "shardReplAddr" -}}
{{- $name := index . 0 -}}
{{- $shardIndex := index . 1 -}}
{{- $replIndex := index . 2 -}}
{{- $domainSuffix := index . 3 -}}
{{- printf "%s-shard%d-data-%d.%s-headless.%s" $name $shardIndex $replIndex $name $domainSuffix -}}
{{- end -}}

{{- define "mongosAddr" -}}
{{- $name := index . 0 -}}
{{- $domainSuffix := index . 1 -}}
{{- printf "%s-mongos-0.%s.%s" $name $name $domainSuffix -}}
{{- end -}}
