{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "cert-manager-webhook-freens.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "cert-manager-webhook-freens.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "cert-manager-webhook-freens.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cert-manager-webhook-freens.selfSignedIssuer" -}}
{{ printf "%s-selfsign" (include "cert-manager-webhook-freens.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-freens.rootCAIssuer" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-freens.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-freens.rootCACertificate" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-freens.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-freens.servingCertificate" -}}
{{ printf "%s-webhook-tls" (include "cert-manager-webhook-freens.fullname" .) }}
{{- end -}}

{{/*
Container image: repository:tag, plus @digest when set. tag defaults to appVersion.
*/}}
{{- define "cert-manager-webhook-freens.image" -}}
{{- $ref := printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- if .Values.image.digest -}}
{{- $ref = printf "%s@%s" $ref .Values.image.digest -}}
{{- end -}}
{{- $ref -}}
{{- end -}}

{{/*
Selector labels: matchLabels, pod template labels and Service selector.
*/}}
{{- define "cert-manager-webhook-freens.selectorLabels" -}}
app: {{ include "cert-manager-webhook-freens.name" . }}
release: {{ .Release.Name }}
{{- end -}}

{{/*
Common labels of every chart object.
*/}}
{{- define "cert-manager-webhook-freens.labels" -}}
app: {{ include "cert-manager-webhook-freens.name" . }}
chart: {{ include "cert-manager-webhook-freens.chart" . }}
release: {{ .Release.Name }}
heritage: {{ .Release.Service }}
{{- end -}}
