{{- define "workloads.replicas" -}}
{{- if eq .root.Values.replicaMode "peak" -}}
{{- .w.peakReplicas -}}
{{- else -}}
{{- .w.replicas -}}
{{- end -}}
{{- end -}}

{{- define "workloads.podTemplate" -}}
metadata:
  labels:
    app: {{ .w.name }}
    podtetris.io/benchmark: "true"
spec:
  terminationGracePeriodSeconds: 1
  tolerations:
    - key: podtetris.io/workload
      operator: Equal
      value: "true"
      effect: NoSchedule
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: type
                operator: In
                values: ["kwok"]
              {{- if .w.pool }}
              - key: podtetris.io/pool
                operator: In
                values: ["{{ .w.pool }}"]
              {{- end }}
    {{- if eq .w.antiAffinity "required" }}
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        - labelSelector:
            matchLabels:
              app: {{ .w.name }}
          topologyKey: kubernetes.io/hostname
    {{- else if eq .w.antiAffinity "preferred" }}
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          podAffinityTerm:
            labelSelector:
              matchLabels:
                app: {{ .w.name }}
            topologyKey: kubernetes.io/hostname
    {{- end }}
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
      imagePullPolicy: IfNotPresent
      resources:
        requests:
          cpu: {{ .w.cpu | quote }}
          memory: {{ .w.memory | quote }}
{{- end -}}
