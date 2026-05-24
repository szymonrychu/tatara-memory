{{/*
Subchart name. Parent chart already namespaces; we use just "lightrag".
*/}}
{{- define "lightrag.name" -}}
{{- default "lightrag" .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lightrag.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "lightrag.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "lightrag.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lightrag.labels" -}}
helm.sh/chart: {{ include "lightrag.chart" . }}
{{ include "lightrag.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: lightrag
app.kubernetes.io/part-of: tatara-memory
{{- end }}

{{- define "lightrag.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lightrag.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Pinned image: repository:tag@digest. Digest must be present.
*/}}
{{- define "lightrag.image" -}}
{{- $r := required "image.repository required" .Values.image.repository -}}
{{- $t := required "image.tag required" .Values.image.tag -}}
{{- $d := required "image.digest required (must be sha256:...)" .Values.image.digest -}}
{{- printf "%s:%s@%s" $r $t $d -}}
{{- end }}

{{- define "lightrag.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "lightrag.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{/*
Non-secret config keys. camelCase values.yaml key -> kebab-case
ConfigMap key. The deployment mounts the ConfigMap via envFrom with
no prefix, so upstream LightRAG env names map 1:1 by uppercasing
and replacing "-" with "_" downstream. We render kebab-case here
because that is the project-wide convention.
*/}}
{{- define "lightrag.configKeys" -}}
llm-binding: {{ .Values.llmBinding | quote }}
llm-model: {{ .Values.llmModel | quote }}
embedding-binding: {{ .Values.embeddingBinding | quote }}
embedding-model: {{ .Values.embeddingModel | quote }}
embedding-dim: {{ .Values.embeddingDim | quote }}
kv-storage: {{ .Values.kvStorage | quote }}
vector-storage: {{ .Values.vectorStorage | quote }}
graph-storage: {{ .Values.graphStorage | quote }}
doc-status-storage: {{ .Values.docStatusStorage | quote }}
neo4j-uri: {{ .Values.neo4jUri | quote }}
max-async: {{ .Values.maxAsync | quote }}
max-parallel-insert: {{ .Values.maxParallelInsert | quote }}
embedding-func-max-async: {{ .Values.embeddingFuncMaxAsync | quote }}
postgres-host: {{ .Values.postgresHost | quote }}
postgres-port: {{ .Values.postgresPort | quote }}
postgres-database: {{ .Values.postgresDatabase | quote }}
postgres-user: {{ .Values.postgresUser | quote }}
{{- end }}

{{/*
Secret resolution: when a secrets.<x>.existingSecret is set, point at
it; otherwise point at our own rendered Secret with the canonical key.
*/}}
{{- define "lightrag.secretName" -}}
{{- include "lightrag.fullname" . -}}
{{- end }}

{{- define "lightrag.openaiSecretName" -}}
{{- default (include "lightrag.secretName" .) .Values.secrets.openai.existingSecret -}}
{{- end }}
{{- define "lightrag.openaiSecretKey" -}}
{{- default "lightrag-openai-api-key" .Values.secrets.openai.existingSecretKey -}}
{{- end }}

{{- define "lightrag.postgresSecretName" -}}
{{- default (include "lightrag.secretName" .) .Values.secrets.postgres.existingSecret -}}
{{- end }}
{{- define "lightrag.postgresSecretKey" -}}
{{- default "postgres-password" .Values.secrets.postgres.existingSecretKey -}}
{{- end }}

{{- define "lightrag.neo4jSecretName" -}}
{{- default (include "lightrag.secretName" .) .Values.secrets.neo4j.existingSecret -}}
{{- end }}
{{- define "lightrag.neo4jSecretKey" -}}
{{- default "neo4j-password" .Values.secrets.neo4j.existingSecretKey -}}
{{- end }}
