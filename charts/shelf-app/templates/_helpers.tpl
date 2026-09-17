{{/*
Labels of a component's pods; used as selector, so they never change.
Argument: dict "app" "component".
*/}}
{{- define "shelf-app.selectorLabels" -}}
shelf.dev/app: {{ .app }}
shelf.dev/component: {{ .component }}
{{- end -}}

{{/*
Labels of every object. Argument: dict "root" "app" "component".
Flux appends the chart digest to the version (0.1.0+abc...); "+" is not allowed in label values.
*/}}
{{- define "shelf-app.labels" -}}
{{ include "shelf-app.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
The environment variable that carries a secret: db-password -> SHELF_SECRET_DB_PASSWORD.
Argument: the secret name.
*/}}
{{- define "shelf-app.secretEnv" -}}
SHELF_SECRET_{{ . | replace "-" "_" | upper }}
{{- end -}}

{{/*
Rewrites ${secrets.<name>} to $(SHELF_SECRET_<NAME>) for kubelet. Every literal $ in the values
is escaped as $$, so the parts between "$$" contain only references.
Argument: dict "s" (the string) "secrets" (the secrets map).
*/}}
{{- define "shelf-app.expand" -}}
{{- $parts := list -}}
{{- range splitList "$$" .s -}}
{{- $part := . -}}
{{- range $name, $_ := $.secrets -}}
{{- $part = replace (printf "${secrets.%s}" $name) (printf "$(%s)" (include "shelf-app.secretEnv" $name)) $part -}}
{{- end -}}
{{- $parts = append $parts $part -}}
{{- end -}}
{{- join "$$" $parts -}}
{{- end -}}

{{/*
The secrets a component references in command, args or env, sorted, as a JSON list.
Argument: dict "comp" "secrets".
*/}}
{{- define "shelf-app.referencedSecrets" -}}
{{- $strings := concat (.comp.command | default list) (.comp.args | default list) (values (.comp.env | default dict)) -}}
{{- $refs := list -}}
{{- range $name, $_ := .secrets -}}
{{- $ref := printf "${secrets.%s}" $name -}}
{{- range $strings -}}
{{- range splitList "$$" (toString .) -}}
{{- if contains $ref . -}}
{{- $refs = append $refs $name -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- uniq $refs | toJson -}}
{{- end -}}

{{/*
An HTTP probe. Argument: dict "health" "failureThreshold".
*/}}
{{- define "shelf-app.probe" -}}
httpGet:
  path: {{ .health.path | quote }}
  port: {{ .health.port }}
{{- with .health.initialDelay }}
initialDelaySeconds: {{ . }}
{{- end }}
periodSeconds: 10
failureThreshold: {{ .failureThreshold }}
{{- end -}}
