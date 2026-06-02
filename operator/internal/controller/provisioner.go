package controller

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

const (
	// defaultAgentRuntimeImage is the agent-runtime image used when the team
	// does not pin one (v2 §3.1 runtime.image.agentRuntime).
	defaultAgentRuntimeImage = "ghcr.io/karo/agent-runtime:v2"
	// defaultSecretKey is the Secret key holding a backend DSN when secretRef
	// omits an explicit key (v2 §4.1).
	defaultSecretKey = "dsn"
	// runIDAnnotation optionally carries the run a provisioned pod participates
	// in; surfaced as KARO_RUN_ID (v2 §4.1).
	runIDAnnotation = "karo.dev/run-id"
	// objectiveAnnotation carries the objective to drive the team with; surfaced
	// as KARO_OBJECTIVE so the agent pods' Coordinator loop has work (v2 §4.1).
	objectiveAnnotation = "karo.dev/objective"
	// teamSpecMount is where the projected full team spec is mounted.
	teamSpecMount = "/etc/karo/team.json"

	labelTeam  = "karo.dev/team"
	labelAgent = "karo.dev/agent"
)

// teamConfigMapName is the single per-team spec ConfigMap mounted into every
// agent pod (the full compiled AgentTeam the shared karo-runtime compiler loads).
func teamConfigMapName(team *karov1.AgentTeam) string {
	return team.Name + "-spec"
}

// teamDocJSON projects the full AgentTeam as a document the shared karo-runtime
// compiler (`compile_flat`) can load: apiVersion/kind/metadata/spec. The
// operator-only `runtime` block is omitted — it is not part of the shared spec
// the Python compiler models (v2 §3.1 / CLI §4.2).
func teamDocJSON(team *karov1.AgentTeam) (string, error) {
	spec := team.Spec
	spec.Runtime = nil
	doc := map[string]interface{}{
		"apiVersion": "karo.dev/v1",
		"kind":       "AgentTeam",
		"metadata":   map[string]interface{}{"name": team.Name},
		"spec":       spec,
	}
	b, err := json.Marshal(doc)
	return string(b), err
}

// workloadName is the deterministic per-agent workload name (<team>-<agent>).
func workloadName(team *karov1.AgentTeam, agent karov1.Agent) string {
	return fmt.Sprintf("%s-%s", team.Name, agent.Name)
}

// scaleToZero reports whether the team uses scale-to-zero. The default is TRUE
// when runtime or runtime.scaleToZero is unset (v2 §3.1 default).
func scaleToZero(team *karov1.AgentTeam) bool {
	if team.Spec.Runtime == nil || team.Spec.Runtime.ScaleToZero == nil {
		return true
	}
	return *team.Spec.Runtime.ScaleToZero
}

// terminalTaskStates are the AgentTask states that need no running pod.
var terminalTaskStates = map[string]bool{"done": true, "failed": true, "cancelled": true}

// taskOwner is the agent a task belongs to (status wins over spec once assigned).
func taskOwner(t *karov1.AgentTask) string {
	if t.Status.Owner != "" {
		return t.Status.Owner
	}
	return t.Spec.Owner
}

// taskState defaults a blank projection to pending (the AgentTask controller
// stamps the same default).
func taskState(t *karov1.AgentTask) string {
	if t.Status.State == "" {
		return "pending"
	}
	return t.Status.State
}

// replicasForAgent is the scale-from-zero decision (v2 §5.1). With scale-to-zero
// off, every agent runs (1). Otherwise an agent is woken (1) when a non-terminal
// AgentTask is owned by it — and the lead is woken whenever *any* work exists, so
// it can plan/claim and hand unowned tasks out. Guard-paused tasks are
// non-terminal, so an agent awaiting attach stays up (attach always has a
// target). No work → 0 (the pod is reclaimed).
func replicasForAgent(team *karov1.AgentTeam, agent karov1.Agent, tasks []karov1.AgentTask) int32 {
	if !scaleToZero(team) {
		return 1
	}
	anyWork := false
	for i := range tasks {
		if terminalTaskStates[taskState(&tasks[i])] {
			continue
		}
		anyWork = true
		if taskOwner(&tasks[i]) == agent.Name {
			return 1
		}
	}
	if anyWork && agent.Name == team.Spec.Coordination.Lead {
		return 1
	}
	return 0
}

// teamTasks lists the AgentTask projections in the team's namespace that belong
// to this team (the scale-from-zero work signal; `karo sling --context` and the
// pods' projection writes land here).
func (r *AgentTeamReconciler) teamTasks(ctx context.Context, team *karov1.AgentTeam) ([]karov1.AgentTask, error) {
	var all karov1.AgentTaskList
	if err := r.List(ctx, &all, client.InNamespace(team.Namespace)); err != nil {
		return nil, err
	}
	out := make([]karov1.AgentTask, 0, len(all.Items))
	for i := range all.Items {
		if all.Items[i].Spec.Team == team.Name {
			out = append(out, all.Items[i])
		}
	}
	return out, nil
}

// agentLabels are the common selector/identity labels for an agent workload.
func agentLabels(team *karov1.AgentTeam, agent karov1.Agent) map[string]string {
	return map[string]string{
		labelTeam:  team.Name,
		labelAgent: agent.Name,
	}
}

// agentImage resolves the agent-runtime image for the team.
func agentImage(team *karov1.AgentTeam) string {
	if team.Spec.Runtime != nil && team.Spec.Runtime.Image != nil && team.Spec.Runtime.Image.AgentRuntime != "" {
		return team.Spec.Runtime.Image.AgentRuntime
	}
	return defaultAgentRuntimeImage
}

// agentPullPolicy resolves the image pull policy (empty → let K8s default).
func agentPullPolicy(team *karov1.AgentTeam) corev1.PullPolicy {
	if team.Spec.Runtime != nil && team.Spec.Runtime.Image != nil && team.Spec.Runtime.Image.PullPolicy != "" {
		return corev1.PullPolicy(team.Spec.Runtime.Image.PullPolicy)
	}
	return ""
}

// otelEndpoint returns the tracing endpoint if configured (v2 §9).
func otelEndpoint(team *karov1.AgentTeam) string {
	rt := team.Spec.Runtime
	if rt != nil && rt.Observability != nil && rt.Observability.Tracing != nil {
		return rt.Observability.Tracing.Endpoint
	}
	return ""
}

// backendEnv builds a SecretKeyRef-sourced env var for a backend DSN, or nil if
// the backend / its secretRef is unset (v2 §4.1).
func backendEnv(name string, b *karov1.Backend) *corev1.EnvVar {
	if b == nil || b.SecretRef == nil || b.SecretRef.Name == "" {
		return nil
	}
	key := b.SecretRef.Key
	if key == "" {
		key = defaultSecretKey
	}
	return &corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: b.SecretRef.Name},
				Key:                  key,
			},
		},
	}
}

// agentEnv assembles the bootstrap env for an agent pod (v2 §4.1).
func agentEnv(team *karov1.AgentTeam, agent karov1.Agent) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "KARO_TEAM", Value: team.Name},
		{Name: "KARO_AGENT", Value: agent.Name},
		{Name: "KARO_RUN_ID", Value: team.Annotations[runIDAnnotation]},
		{Name: "KARO_SPEC_PATH", Value: teamSpecMount},
		{Name: "KARO_OBJECTIVE", Value: team.Annotations[objectiveAnnotation]},
	}
	if ep := otelEndpoint(team); ep != "" {
		env = append(env, corev1.EnvVar{Name: "KARO_OTEL_ENDPOINT", Value: ep})
	}

	var backends *karov1.Backends
	if team.Spec.Runtime != nil {
		backends = team.Spec.Runtime.Backends
	}
	if backends != nil {
		if e := backendEnv("KARO_TASKS_DSN", backends.Tasks); e != nil {
			env = append(env, *e)
		}
		if e := backendEnv("KARO_MEMORY_DSN", backends.Memory); e != nil {
			env = append(env, *e)
		}
		if e := backendEnv("KARO_MAILBOX_DSN", backends.Mailbox); e != nil {
			env = append(env, *e)
		}
	}
	return env
}

// teamConfigMap projects the full compiled team for the pods to load (mounted at
// /etc/karo/team.json). Every agent pod mounts the same ConfigMap and runs the
// karo-runtime Coordinator scoped to its own agent. Orchestration only — the data
// is the compiled spec, no reasoning logic (v2 §4.1, §12).
func teamConfigMap(team *karov1.AgentTeam) (*corev1.ConfigMap, error) {
	doc, err := teamDocJSON(team)
	if err != nil {
		return nil, err
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      teamConfigMapName(team),
			Namespace: team.Namespace,
			Labels:    map[string]string{labelTeam: team.Name},
		},
		Data: map[string]string{"team": team.Name, "team.json": doc},
	}, nil
}

// agentDeployment builds the per-agent Deployment running the agent-runtime
// image under the bootstrap contract (v2 §4.1). Replicas are 0 under
// scale-to-zero (the Dispatcher scales up on demand), else 1.
func agentDeployment(team *karov1.AgentTeam, agent karov1.Agent) *appsv1.Deployment {
	var replicas int32 = 1
	if scaleToZero(team) {
		replicas = 0
	}

	labels := agentLabels(team, agent)
	nonRoot := true
	readOnly := true
	noPrivEsc := false
	var runAsUser int64 = 65532

	container := corev1.Container{
		Name:  "agent",
		Image: agentImage(team),
		Env:   agentEnv(team, agent),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "spec", MountPath: "/etc/karo", ReadOnly: true},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             &nonRoot,
			RunAsUser:                &runAsUser,
			ReadOnlyRootFilesystem:   &readOnly,
			AllowPrivilegeEscalation: &noPrivEsc,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
	if pp := agentPullPolicy(team); pp != "" {
		container.ImagePullPolicy = pp
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workloadName(team, agent),
			Namespace: team.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
					Volumes: []corev1.Volume{{
						Name: "spec",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: teamConfigMapName(team),
								},
							},
						},
					}},
				},
			},
		},
	}
}

// provisionAgents reconciles the per-agent ConfigMap + Deployment for every
// agent in the team and returns how many deployments currently have at least
// one ready replica — the authoritative "active agents" count for status
// (v2 §5.4). Orchestration only; no agent reasoning (v2 §12).
func (r *AgentTeamReconciler) provisionAgents(ctx context.Context, team *karov1.AgentTeam) (int, error) {
	active := 0

	// One per-team spec ConfigMap mounted into every agent pod.
	teamCM, err := teamConfigMap(team)
	if err != nil {
		return active, fmt.Errorf("project team spec: %w", err)
	}
	desiredCM := teamCM.DeepCopy()
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, teamCM, func() error {
		teamCM.Data = desiredCM.Data
		teamCM.Labels = desiredCM.Labels
		return controllerutil.SetControllerReference(team, teamCM, r.Scheme)
	}); err != nil {
		return active, fmt.Errorf("team configmap: %w", err)
	}

	// The scale-from-zero work signal: non-terminal AgentTasks in the namespace.
	tasks, err := r.teamTasks(ctx, team)
	if err != nil {
		return active, fmt.Errorf("list tasks: %w", err)
	}

	for _, agent := range team.Spec.Agents {
		replicas := replicasForAgent(team, agent, tasks)
		dep := agentDeployment(team, agent)
		dep.Spec.Replicas = &replicas
		desired := dep.Spec.DeepCopy()
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
			dep.Labels = agentLabels(team, agent)
			dep.Spec.Replicas = desired.Replicas
			dep.Spec.Selector = desired.Selector
			dep.Spec.Template = desired.Template
			return controllerutil.SetControllerReference(team, dep, r.Scheme)
		}); err != nil {
			return active, fmt.Errorf("deployment for agent %q: %w", agent.Name, err)
		}

		// Re-read to observe the latest ReadyReplicas (CreateOrUpdate mutates the
		// passed object with the applied spec but does not refresh status).
		var observed appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: dep.Namespace, Name: dep.Name}, &observed); err != nil {
			return active, fmt.Errorf("get deployment for agent %q: %w", agent.Name, err)
		}
		if observed.Status.ReadyReplicas >= 1 {
			active++
		}
	}
	return active, nil
}
