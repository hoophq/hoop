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

{{/*
One probe of the gateway container, or nothing when it is off. Takes
(list KIND value), KIND readinessProbe or startupProbe. The defaults are here,
not in values.yaml: a release from an older chart upgraded with
--reuse-values has no probe key, and must still get the probe. Absent or null
"enabled" takes the default; only false turns readiness off. With no handler,
the probe opens a TCP connection to the port "api", which the gateway opens
last in its startup.
*/}}
{{- define "hoopgateway.probe" -}}
{{- $kind := index . 0 -}}
{{- $value := index . 1 | default dict -}}
{{- $defaults := get (dict
      "readinessProbe" (dict "enabled" true "periodSeconds" 10 "timeoutSeconds" 5 "failureThreshold" 3)
      "startupProbe" (dict "enabled" false "periodSeconds" 10 "timeoutSeconds" 5 "failureThreshold" 360)) $kind -}}
{{- /* Not merge for "enabled": merge reads false as unset. */ -}}
{{- $enabled := $defaults.enabled -}}
{{- if not (kindIs "invalid" $value.enabled) -}}
{{- $enabled = $value.enabled -}}
{{- end -}}
{{- if $enabled -}}
{{- $probe := merge (omit $value "enabled") (omit $defaults "enabled") -}}
{{- if not (or $probe.httpGet $probe.tcpSocket $probe.exec $probe.grpc) -}}
{{- $_ := set $probe "tcpSocket" (dict "port" "api") -}}
{{- end -}}
{{ $kind }}:
{{ toYaml $probe | indent 2 }}
{{- end -}}
{{- end -}}
