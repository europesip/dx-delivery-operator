{{- /* nameOverride: keep the resource names of 0.12.x (hcl-dx-scriptapp-operator) on an existing install */ -}}
{{- define "dxop.name" -}}{{ default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- define "dxop.fullname" -}}
{{- if .Values.fullnameOverride -}}{{ .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else -}}
{{- $name := include "dxop.name" . -}}
{{- if contains $name .Release.Name -}}{{ .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else -}}{{ printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- end -}}
{{- end -}}
{{- define "dxop.labels" -}}
app.kubernetes.io/name: {{ include "dxop.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}
{{- define "dxop.selector" -}}
app.kubernetes.io/name: {{ include "dxop.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- define "dxop.serviceAccount" -}}
{{- if .Values.serviceAccount.create -}}{{ default (include "dxop.fullname" .) .Values.serviceAccount.name }}
{{- else -}}{{ default "default" .Values.serviceAccount.name }}{{- end -}}
{{- end -}}

{{/*
Who issues the certificate of the Java extensions webhook (issue #63): cert-manager or openshift-service-ca.
auto: the OpenShift service CA on OpenShift without cert-manager; cert-manager otherwise (also when Helm cannot see the
APIs of the cluster, as with helm template: the behaviour before 0.15.0).
*/}}
{{- define "dxop.webhookCertProvider" -}}
{{- $p := (((.Values.javaExtensions).certificate).provider) | default "auto" -}}
{{- if eq $p "auto" -}}
{{- if and (not (.Capabilities.APIVersions.Has "cert-manager.io/v1")) (or (.Capabilities.APIVersions.Has "route.openshift.io/v1") (.Capabilities.APIVersions.Has "security.openshift.io/v1")) -}}
openshift-service-ca
{{- else -}}
cert-manager
{{- end -}}
{{- else if or (eq $p "cert-manager") (eq $p "openshift-service-ca") -}}
{{ $p }}
{{- else -}}
{{- fail (printf "javaExtensions.certificate.provider must be auto, cert-manager or openshift-service-ca (got %q)" $p) -}}
{{- end -}}
{{- end -}}
