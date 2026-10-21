{{/* Where idpFederatedToken mounts the projected service account token. */}}
{{- define "hoopgateway.idpTokenDir" -}}/var/run/secrets/hoop/idp{{- end -}}

{{/* The client assertion file the gateway reads, or empty. */}}
{{- define "hoopgateway.idpClientAssertionFile" -}}
{{- if (.Values.idpFederatedToken | default dict).enabled -}}
{{ include "hoopgateway.idpTokenDir" . }}/token
{{- else -}}
{{ .Values.config.IDP_CLIENT_ASSERTION_FILE | default "" }}
{{- end -}}
{{- end -}}
