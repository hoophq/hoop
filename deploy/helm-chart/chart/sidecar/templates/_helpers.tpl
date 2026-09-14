{{/*
Where the rendered config document is mounted. One place, because the mount,
the volume and HOOP_SIDECAR_CONFIG all have to agree.
*/}}
{{- define "hoopsidecar.configDir" -}}/etc/hoop-inspect{{- end -}}
{{- define "hoopsidecar.configFile" -}}config.yaml{{- end -}}
{{- define "hoopsidecar.configPath" -}}
{{ include "hoopsidecar.configDir" . }}/{{ include "hoopsidecar.configFile" . }}
{{- end -}}

{{/*
The admin port. Fixed at 19000, the number every config under
deploy/docker-compose/<stack>/sidecar/ binds and the one firstrun.go names as the
convention. It is written literally into the Deployment, the Service and both
probes; this constant exists so the four cannot fall out of step, not to make
the number configurable.

A config that puts admin somewhere else is refused below rather than rendered
around, because the probes would then poll a closed port and fail every pod.
*/}}
{{- define "hoopsidecar.adminPort" -}}19000{{- end -}}

{{/*
Whether a config file is mounted at all. False in control-plane mode, where
the handshake supplies the running config and there is no file to point at.
*/}}
{{- define "hoopsidecar.hasConfigFile" -}}
{{- if or .Values.config .Values.existingConfigMap -}}true{{- end -}}
{{- end -}}

{{/*
Refuse to render a Deployment that cannot start, with the message the relay
itself would print on the restart nobody is watching.
*/}}
{{- define "hoopsidecar.validate" -}}
{{- if and .Values.config .Values.existingConfigMap -}}
{{- fail "set either config or existingConfigMap, not both: the chart would render a ConfigMap it then does not mount" -}}
{{- end -}}
{{- if not (or (include "hoopsidecar.hasConfigFile" .) .Values.controlPlane.url) -}}
{{- fail "no config file was given and no control plane is configured: set config, existingConfigMap or controlPlane.url" -}}
{{- end -}}
{{- if and .Values.controlPlane.url (not .Values.controlPlane.token) -}}
{{- fail "controlPlane.url is set but controlPlane.token is empty: the handshake is refused without it" -}}
{{- end -}}
{{- if and .Values.controlPlane.token (not .Values.controlPlane.url) -}}
{{- fail "controlPlane.token is set but no control plane is configured: set controlPlane.url or drop the token" -}}
{{- end -}}
{{/*
The probes are hardcoded to the admin port, so a config that moves or omits
admin is a pod that never goes ready. Caught here, at render, instead of as a
CrashLoopBackOff at 3am. Only checkable when the chart can see the config:
under controlPlane.url or existingConfigMap this is the operator's to get
right, and the README says so.
*/}}
{{- $admin := include "hoopsidecar.adminPort" . -}}
{{- if .Values.config -}}
{{- $listen := "" -}}
{{- if .Values.config.admin -}}
{{- $listen = .Values.config.admin.listen | default "" -}}
{{- end -}}
{{- if not $listen -}}
{{- fail (printf "config.admin.listen is not set: the chart probes /healthz on %s, and the admin server is disabled when its listen address is empty. Set it to '0.0.0.0:%s'" $admin $admin) -}}
{{- end -}}
{{- if not (hasSuffix (printf ":%s" $admin) $listen) -}}
{{- fail (printf "config.admin.listen is %q but this chart probes /healthz on %s. Use '0.0.0.0:%s'" $listen $admin $admin) -}}
{{- end -}}
{{- end -}}
{{- end -}}
