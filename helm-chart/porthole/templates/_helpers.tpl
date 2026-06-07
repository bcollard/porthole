{{/* Expand the chart name. */}}
{{- define "porthole.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Full resource name. */}}
{{- define "porthole.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "porthole.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "porthole.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Selector labels. */}}
{{- define "porthole.selectorLabels" -}}
app.kubernetes.io/name: {{ include "porthole.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* ServiceAccount name. */}}
{{- define "porthole.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "porthole.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* OPA policy ConfigMap name. */}}
{{- define "porthole.opaConfigMapName" -}}
{{- printf "%s-opa-policy" (include "porthole.fullname" .) -}}
{{- end -}}

{{/* OIDC secret name (chart-managed or user-supplied). */}}
{{- define "porthole.oidcSecretName" -}}
{{- if .Values.gateway.oidc.existingSecretName -}}
{{- .Values.gateway.oidc.existingSecretName -}}
{{- else -}}
{{- printf "%s-oidc" (include "porthole.fullname" .) -}}
{{- end -}}
{{- end -}}
