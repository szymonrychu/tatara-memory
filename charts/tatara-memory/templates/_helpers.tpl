{{/*
Expand the name of the chart.
*/}}
{{- define "tatara-memory.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "tatara-memory.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "tatara-memory.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "tatara-memory.labels" -}}
helm.sh/chart: {{ include "tatara-memory.chart" . }}
{{ include "tatara-memory.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "tatara-memory.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tatara-memory.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "tatara-memory.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "tatara-memory.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Map camelCase values.* scalars to SCREAMING_SNAKE ConfigMap keys.
Strict: values.yaml carries only scalars; this macro is the single mapping point.
*/}}
{{- define "tatara-memory.envConfig" -}}
HTTP_ADDR: {{ .Values.httpAddr | quote }}
LIGHTRAG_BASE_URL: {{ .Values.lightragBaseUrl | quote }}
OIDC_ISSUER: {{ .Values.oidcIssuer | quote }}
OIDC_AUDIENCE: {{ .Values.oidcAudience | quote }}
WORKER_POOL_SIZE: {{ .Values.workerPoolSize | quote }}
INGEST_ITEM_TIMEOUT: {{ .Values.ingestItemTimeout | quote }}
LOG_LEVEL: {{ .Values.logLevel | quote }}
OTLP_ENDPOINT: {{ .Values.otlpEndpoint | quote }}
{{- end -}}

{{/*
Env for the opt-in eval CronJob (issue #46). The eval binary reads MEMORY_BASE_URL
(its target), EVAL_PUSH_URL (operator push-receiver), and LOG_LEVEL. Kept separate
from envConfig so the eval pod targets a dedicated eval-memory URL, not this app.
*/}}
{{- define "tatara-memory.evalEnvConfig" -}}
MEMORY_BASE_URL: {{ .Values.eval.targetBaseUrl | quote }}
EVAL_PUSH_URL: {{ .Values.eval.pushUrl | quote }}
LOG_LEVEL: {{ .Values.logLevel | quote }}
{{- end -}}
