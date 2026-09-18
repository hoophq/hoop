{{- define "hoopcontrolplane.name" -}}
{{- default "hoopcontrolplane" .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hoopcontrolplane.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "hoopcontrolplane.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "hoopcontrolplane.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hoopcontrolplane.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "hoopcontrolplane.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "hoopcontrolplane.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "hoopcontrolplane.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "hoopcontrolplane.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- .Values.serviceAccount.name | default "" -}}
{{- end -}}
{{- end -}}

{{- define "hoopcontrolplane.configSecretName" -}}
{{- printf "%s-config" (include "hoopcontrolplane.fullname" .) -}}
{{- end -}}

{{- define "hoopcontrolplane.extraSecretName" -}}
{{- printf "%s-extra-env" (include "hoopcontrolplane.fullname" .) -}}
{{- end -}}

{{- define "hoopcontrolplane.httpPort" -}}8009{{- end -}}

{{- define "hoopcontrolplane.basePath" -}}
{{- $u := .Values.config.API_URL | default "" | trimSuffix "/" -}}
{{- regexReplaceAll "^[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*" $u "" -}}
{{- end -}}

{{- define "hoopcontrolplane.validate" -}}
{{- $c := .Values.config | default dict -}}

{{- if not $c.POSTGRES_DB_URI -}}
{{- fail "config.POSTGRES_DB_URI is required: the control plane runs migrations and the org bootstrap before it listens" -}}
{{- end -}}
{{- if not $c.API_URL -}}
{{- fail "config.API_URL is required: it is the address the web app and every sidecar reach this deployment on" -}}
{{- end -}}

{{- if hasPrefix "pglite://" $c.POSTGRES_DB_URI -}}
{{- fail "config.POSTGRES_DB_URI uses pglite://, which is single-node and serves one connection at a time. Point at an external PostgreSQL" -}}
{{- end -}}

{{- if not (regexMatch "^https?://" $c.API_URL) -}}
{{- fail (printf "config.API_URL is %q but must start with http:// or https://. Without a scheme the hostname is parsed as a URL path and every route is mounted under it" $c.API_URL) -}}
{{- end -}}

{{- $port := include "hoopcontrolplane.httpPort" . -}}
{{- if and $c.PORT (ne (toString $c.PORT) $port) -}}
{{- fail (printf "config.PORT is %v but this chart publishes %s. Leave PORT unset" $c.PORT $port) -}}
{{- end -}}

{{- if $c.IDP_ISSUER -}}
{{- if not $c.IDP_CLIENT_ID -}}
{{- fail "config.IDP_CLIENT_ID is required when config.IDP_ISSUER is set" -}}
{{- end -}}
{{- if not $c.IDP_CLIENT_SECRET -}}
{{- fail "config.IDP_CLIENT_SECRET is required when config.IDP_ISSUER is set" -}}
{{- end -}}
{{- end -}}

{{- if or (hasKey .Values.image "command") (hasKey .Values.image "args") -}}
{{- fail "image.command and image.args are not supported by this chart: the image runs `hoop start control-plane` from its own CMD behind a tini ENTRYPOINT. Overriding the command can boot a full gateway instead, and overriding the entrypoint drops tini, which this process needs in order to stop on SIGTERM. Point image.repository/image.tag at an image whose CMD is already correct" -}}
{{- end -}}

{{- if and .Values.gatewayApi.enabled (not .Values.gatewayApi.createGateway) (not .Values.gatewayApi.httpRoute.parentRefs) -}}
{{- fail "gatewayApi.createGateway is false but gatewayApi.httpRoute.parentRefs is empty: name the Gateway to attach to, or set createGateway" -}}
{{- end -}}

{{- if and .Values.gatewayApi.enabled (not .Values.service.enabled) (not .Values.gatewayApi.httpRoute.rules) -}}
{{- fail "gatewayApi.enabled is true and service.enabled is false, but gatewayApi.httpRoute.rules is empty: the default route points at the Service this chart would have created, so it would resolve to nothing. Set service.enabled, or set rules naming a backend of your own" -}}
{{- end -}}
{{- end -}}
