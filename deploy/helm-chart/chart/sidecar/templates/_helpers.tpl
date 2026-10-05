{{/*
Release-aware names. The sidecar chart is installed once per upstream it
fronts — the README says as many times as you have upstreams — so every
chart-owned resource carries the release name. Two installs in one namespace
must not fight over a Deployment called "hoopsidecar".

The base is the literal "hoopsidecar" rather than .Chart.Name, which is
"hoopsidecar-chart" and would read as a duplicate in every resource name. A
release named after the chart collapses to the bare name, so
`helm install hoopsidecar ...` still produces "hoopsidecar" and not
"hoopsidecar-hoopsidecar".
*/}}
{{- define "hoopsidecar.name" -}}
{{- default "hoopsidecar" .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hoopsidecar.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "hoopsidecar.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Selector labels. The instance label is what isolates one release from another:
without it a second install's Deployment selects the first install's pods, and
its Service sends a client's Postgres session to the wrong upstream.

These go into the Deployment's selector, which is IMMUTABLE once applied. They
are settled now, before the chart's first release, and must not be edited after
one: a changed selector makes `helm upgrade` fail on an existing install.
*/}}
{{- define "hoopsidecar.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hoopsidecar.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "hoopsidecar.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "hoopsidecar.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
The ServiceAccount the pod runs as. `serviceAccount.name` names one this chart
does not create — the shape GKE Workload Identity wants, which is how an
analyzer reaches Vertex with no credential on disk.
*/}}
{{- define "hoopsidecar.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "hoopsidecar.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- .Values.serviceAccount.name | default "" -}}
{{- end -}}
{{- end -}}

{{/*
Names of the resources this chart owns. The ConfigMap is the exception the
caller may override: `existingConfigMap` names one somebody else made, so it is
used verbatim and never rewritten through fullname.
*/}}
{{- define "hoopsidecar.configMapName" -}}
{{- if .Values.existingConfigMap -}}
{{- .Values.existingConfigMap -}}
{{- else -}}
{{- printf "%s-config" (include "hoopsidecar.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "hoopsidecar.envSecretName" -}}
{{- printf "%s-env" (include "hoopsidecar.fullname" .) -}}
{{- end -}}

{{- define "hoopsidecar.extraEnvSecretName" -}}
{{- printf "%s-extra-env" (include "hoopsidecar.fullname" .) -}}
{{- end -}}

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
Where the projected service account token lands under
controlPlane.identity: kubernetes. Its own directory, never the default
service account mount: that token's audience is the API server, and the
control plane must refuse it.
*/}}
{{- define "hoopsidecar.identityTokenDir" -}}/var/run/hoop-sidecar{{- end -}}
{{- define "hoopsidecar.identityTokenPath" -}}{{ include "hoopsidecar.identityTokenDir" . }}/token{{- end -}}

{{/*
The identity token's audience. A control plane serving several
organizations maps each (issuer, audience) pair to one organization, so each
one sets its own; a single organization leaves it empty and gets the URL.
*/}}
{{- define "hoopsidecar.identityAudience" -}}
{{- .Values.controlPlane.identityAudience | default .Values.controlPlane.url -}}
{{- end -}}

{{/*
The projected token's lifetime as an integer, or "" when the value is not
one. A values file parses every number as float64, so a whole float counts;
it is printed through int64 so a large value never renders as 1e+06.
*/}}
{{- define "hoopsidecar.identityTokenExpiration" -}}
{{- $exp := .Values.controlPlane.tokenExpirationSeconds | default 3600 -}}
{{- if kindIs "string" $exp -}}
{{- if regexMatch "^[1-9][0-9]*$" $exp -}}{{ $exp }}{{- end -}}
{{- else if or (kindIs "int" $exp) (kindIs "int64" $exp) -}}
{{- $exp -}}
{{- else if kindIs "float64" $exp -}}
{{- if eq $exp (float64 (int64 $exp)) -}}{{ int64 $exp }}{{- end -}}
{{- end -}}
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
{{- $cp := .Values.controlPlane -}}
{{- if and $cp.identity (not (has $cp.identity (list "kubernetes" "gcp"))) -}}
{{- fail (printf "controlPlane.identity is %q: use kubernetes or gcp, or leave it empty" $cp.identity) -}}
{{- end -}}
{{- if and $cp.token $cp.identity -}}
{{- fail "set controlPlane.token or controlPlane.identity, not both: the relay refuses two credentials" -}}
{{- end -}}
{{- if and $cp.url (not (or $cp.token $cp.identity)) -}}
{{- fail "controlPlane.url is set but neither controlPlane.token nor controlPlane.identity is: the handshake is refused without a credential" -}}
{{- end -}}
{{- if and (or $cp.token $cp.identity) (not $cp.url) -}}
{{- fail "a control plane credential is set but no control plane is configured: set controlPlane.url or drop the credential" -}}
{{- end -}}
{{- if and $cp.identityAudience (not $cp.identity) -}}
{{- fail "controlPlane.identityAudience is set but controlPlane.identity is not: only a service account identity token has an audience" -}}
{{- end -}}
{{- if eq $cp.identity "kubernetes" -}}
{{- $exp := include "hoopsidecar.identityTokenExpiration" . -}}
{{- if not $exp -}}
{{- fail (printf "controlPlane.tokenExpirationSeconds is %v: it must be an integer number of seconds, 600 or more (the Kubernetes minimum)" $cp.tokenExpirationSeconds) -}}
{{- end -}}
{{- if lt (atoi $exp) 600 -}}
{{- fail (printf "controlPlane.tokenExpirationSeconds is %s: Kubernetes refuses a projected token that expires in less than 600 seconds; set 600 or more" $exp) -}}
{{- end -}}
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
