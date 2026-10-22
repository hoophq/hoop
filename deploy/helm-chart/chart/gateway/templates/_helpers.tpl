{{/* Where idpFederatedToken mounts the projected service account token. */}}
{{- define "hoopgateway.idpTokenDir" -}}/var/run/secrets/hoop/idp{{- end -}}

{{/*
required, unless existingSecret is set: the key can come from that Secret,
which the chart cannot read. Takes (list . "message" value).
*/}}
{{- define "hoopgateway.required" -}}
{{- if (index . 0).Values.existingSecret -}}
{{ index . 2 }}
{{- else -}}
{{ required (index . 1) (index . 2) }}
{{- end -}}
{{- end -}}

{{/* The client assertion file the gateway reads, or empty. */}}
{{- define "hoopgateway.idpClientAssertionFile" -}}
{{- if (.Values.idpFederatedToken | default dict).enabled -}}
{{ include "hoopgateway.idpTokenDir" . }}/token
{{- else -}}
{{ .Values.config.IDP_CLIENT_ASSERTION_FILE | default "" }}
{{- end -}}
{{- end -}}
