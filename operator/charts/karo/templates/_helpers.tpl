{{- define "karo.name" -}}
karo
{{- end -}}

{{- define "karo.labels" -}}
app.kubernetes.io/name: {{ include "karo.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: karo
{{- end -}}

{{- define "karo.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ .Values.serviceAccount.name }}
{{- else -}}
default
{{- end -}}
{{- end -}}
