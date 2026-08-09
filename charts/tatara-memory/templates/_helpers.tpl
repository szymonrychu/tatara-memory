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
HTTP_WRITE_TIMEOUT: {{ .Values.httpWriteTimeout | quote }}
INGEST_CREATE_JOB_TIMEOUT: {{ .Values.ingestCreateJobTimeout | quote }}
MEMORY_PURGE_BUDGET: {{ .Values.memoryPurgeBudget | quote }}
DB_MAX_OPEN_CONNS: {{ .Values.dbMaxOpenConns | quote }}
DB_MAX_IDLE_CONNS: {{ .Values.dbMaxIdleConns | quote }}
MEMORIES_BULK_MAX_IN_FLIGHT: {{ .Values.memoriesBulkMaxInFlight | quote }}
CODE_GRAPH_BULK_MAX_IN_FLIGHT: {{ .Values.codeGraphBulkMaxInFlight | quote }}
ADMISSION_WAIT: {{ .Values.admissionWait | quote }}
ADMISSION_RETRY_AFTER: {{ .Values.admissionRetryAfter | quote }}
DB_CONN_MAX_LIFETIME: {{ .Values.dbConnMaxLifetime | quote }}
DB_CONN_MAX_IDLE_TIME: {{ .Values.dbConnMaxIdleTime | quote }}
PG_STATEMENT_TIMEOUT: {{ .Values.pgStatementTimeout | quote }}
PG_IDLE_IN_TRANSACTION_TIMEOUT: {{ .Values.pgIdleInTransactionTimeout | quote }}
PG_LOCK_TIMEOUT: {{ .Values.pgLockTimeout | quote }}
ANALYTICS_MAX_CONCURRENCY: {{ .Values.analyticsMaxConcurrency | quote }}
ANALYTICS_RECOMPUTE_TIMEOUT: {{ .Values.analyticsRecomputeTimeout | quote }}
DB_WAIT_TIMEOUT: {{ .Values.dbWaitTimeout | quote }}
DB_WAIT_INTERVAL: {{ .Values.dbWaitInterval | quote }}
LOG_LEVEL: {{ .Values.logLevel | quote }}
OTLP_ENDPOINT: {{ .Values.otlpEndpoint | quote }}
{{- end -}}

{{/*
Parse a binary size into whole KiB.

Accepts the Kubernetes quantity suffixes Ki/Mi/Gi/Ti and the PostgreSQL
memory-unit suffixes kB/MB/GB/TB, which denote the same binary multiples, so
a PVC size and a postgresql.conf size can be compared against each other.
Decimal SI suffixes (K/M/G) and bare byte counts are REJECTED rather than
silently approximated - the only caller is a headroom check whose entire job
is to be exact.
*/}}
{{- define "tatara-memory.kib" -}}
{{- $q := . | toString | trim -}}
{{- $n := regexFind "^[0-9]+" $q -}}
{{- if not $n -}}
{{- fail (printf "tatara-memory: cannot parse size %q: expected digits followed by Ki, Mi, Gi, Ti, kB, MB, GB or TB" $q) -}}
{{- end -}}
{{- $unit := trimPrefix $n $q -}}
{{- $mult := index (dict "Ki" 1 "kB" 1 "Mi" 1024 "MB" 1024 "Gi" 1048576 "GB" 1048576 "Ti" 1073741824 "TB" 1073741824) $unit -}}
{{- if not $mult -}}
{{- fail (printf "tatara-memory: size %q uses unit %q: use a binary unit (Ki, Mi, Gi, Ti, kB, MB, GB, TB) so the WAL headroom check stays exact" $q $unit) -}}
{{- end -}}
{{- mul (int64 $n) $mult -}}
{{- end -}}

{{/*
Refuse to render a postgres cluster that is missing its storage contract.

The cnpg/cluster subchart happily defaults to an 8Gi PGDATA volume with NO
dedicated WAL volume and NO bound on replication-slot WAL retention. Every
one of those defaults has taken a tatara memory backend down:
tatara-operator#238 (WAL filled the shared data volume), the 2026-07-06
replica crashloop (WAL volume too small for a standby resync), and
tatara-helmfile#263 (an orphan slot pinned WAL with nothing to cap it).

An unset size is therefore a deploy-time error, never an inherited default,
and max_slot_wal_keep_size must leave at least half the WAL volume free for
checkpoint WAL and archive backlog.
*/}}
{{- define "tatara-memory.validatePostgres" -}}
{{- $pg := .Values.postgres | default dict -}}
{{- if $pg.enabled -}}
{{- $c := $pg.cluster | default dict -}}
{{- if not (dig "storage" "size" "" $c) -}}
{{- fail "tatara-memory: postgres.cluster.storage.size is required; the cnpg default (8Gi) is not a sizing decision" -}}
{{- end -}}
{{- if not (dig "walStorage" "enabled" false $c) -}}
{{- fail "tatara-memory: postgres.cluster.walStorage.enabled must be true; with it off pg_wal shares the PGDATA volume and a WAL burst takes writes down (tatara-operator#238)" -}}
{{- end -}}
{{- $wal := dig "walStorage" "size" "" $c -}}
{{- if not $wal -}}
{{- fail "tatara-memory: postgres.cluster.walStorage.size is required when walStorage is enabled" -}}
{{- end -}}
{{- $keep := dig "postgresql" "parameters" "max_slot_wal_keep_size" "" $c -}}
{{- if not $keep -}}
{{- fail "tatara-memory: postgres.cluster.postgresql.parameters.max_slot_wal_keep_size is required; without it one stuck or diverged standby's replication slot pins WAL until the volume fills (tatara-helmfile#263)" -}}
{{- end -}}
{{- $walKiB := include "tatara-memory.kib" $wal | int64 -}}
{{- $keepKiB := include "tatara-memory.kib" $keep | int64 -}}
{{- if gt $keepKiB (div $walKiB 2) -}}
{{- fail (printf "tatara-memory: max_slot_wal_keep_size (%s) exceeds half of walStorage.size (%s); replication-slot retention alone could fill the WAL volume, leaving nothing for checkpoint WAL" $keep $wal) -}}
{{- end -}}
{{- end -}}
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
