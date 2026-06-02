package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

	// gitSecretKey is the well-known key in runtime.secrets whose SecretRef names
	// the Secret holding git credentials (a GITHUB_TOKEN, and optional
	// GIT_AUTHOR_NAME/GIT_AUTHOR_EMAIL) for cluster repo clone + push. This is the
	// cluster equivalent of "auth is the runner's own git config" locally: on a
	// cluster there is no human runner, so the team works as a service account
	// (docs/DEV-WORKFLOW.md §5b). The team spec carries only the *reference*, never
	// the credential — so it stays shareable.
	gitSecretKey = "git"
	// workspaceMount is the shared repo workspace (the cluster equivalent of the
	// local ./workspace). The clone init-container writes here; the agent
	// container works here. emptyDir, so it is writable under a read-only rootfs.
	workspaceMount = "/workspace"
	// homeMount is a writable HOME for git/gh config (credential helper, identity)
	// shared between the init-container and the agent container so a `git push`
	// from the open-pr skill reuses the credentials the init-container set up.
	homeMount = "/home/agent"

	workspaceVolume = "workspace"
	homeVolume      = "agent-home"

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

// gitSecretName returns the name of the Secret holding git credentials
// (runtime.secrets["git"]), or "" if the team declares none. Used to wire clone
// + push auth as a service account on cluster (docs/DEV-WORKFLOW.md §5b).
func gitSecretName(team *karov1.AgentTeam) string {
	if team.Spec.Runtime == nil || team.Spec.Runtime.Secrets == nil {
		return ""
	}
	if ref, ok := team.Spec.Runtime.Secrets[gitSecretKey]; ok {
		return ref.Name
	}
	return ""
}

// teamRepos returns the repos declared at the team level (spec.resources.repos).
func teamRepos(team *karov1.AgentTeam) []karov1.Repo {
	if team.Spec.Resources == nil {
		return nil
	}
	return team.Spec.Resources.Repos
}

// agentRepos resolves the subset of team repos this agent works on (its
// frontmatter `repos:` names), mirroring the local `ensure_repos(only_agents=)`
// scoping so a pod clones only what its agent needs.
func agentRepos(team *karov1.AgentTeam, agent karov1.Agent) []karov1.Repo {
	if len(agent.Repos) == 0 {
		return nil
	}
	byName := make(map[string]karov1.Repo)
	for _, r := range teamRepos(team) {
		byName[r.Name] = r
	}
	out := make([]karov1.Repo, 0, len(agent.Repos))
	for _, n := range agent.Repos {
		if r, ok := byName[n]; ok {
			out = append(out, r)
		}
	}
	return out
}

// repoDest is the clone path under the workspace for a repo (its `path:` override
// or its name), mirroring the local ./workspace/<name> convention.
func repoDest(r karov1.Repo) string {
	sub := r.Path
	if sub == "" {
		sub = r.Name
	}
	return workspaceMount + "/" + sub
}

// agentWorkingDir mirrors the local rule (CLI §3e): an agent with exactly one
// repo runs *inside* it; with several it runs in the workspace root.
func agentWorkingDir(repos []karov1.Repo) string {
	if len(repos) == 1 {
		return repoDest(repos[0])
	}
	return workspaceMount
}

// gitEnvFrom exposes every key of the git-credentials Secret (GITHUB_TOKEN,
// GIT_AUTHOR_NAME, GIT_AUTHOR_EMAIL, …) as env vars in a container, or nil when
// the team declares no git secret (public repos still clone, just unauthenticated).
func gitEnvFrom(secret string) []corev1.EnvFromSource {
	if secret == "" {
		return nil
	}
	return []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret},
		},
	}}
}

// cloneScript renders the init-container's clone/update script. The token is
// supplied at runtime via a credential helper (so it is never baked into the
// manifest) and the global git config lives on the shared home volume, so the
// agent container's later `git push` reuses the same credentials and identity.
func cloneScript(repos []karov1.Repo, hasSecret bool) string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	b.WriteString("export HOME=" + homeMount + "\n")
	b.WriteString("git config --global --add safe.directory '*'\n")
	if hasSecret {
		b.WriteString("git config --global credential.helper " +
			"'!f() { echo username=x-access-token; echo \"password=$GITHUB_TOKEN\"; }; f'\n")
		b.WriteString("[ -n \"${GIT_AUTHOR_NAME:-}\" ] && git config --global user.name \"$GIT_AUTHOR_NAME\" || true\n")
		b.WriteString("[ -n \"${GIT_AUTHOR_EMAIL:-}\" ] && git config --global user.email \"$GIT_AUTHOR_EMAIL\" || true\n")
	}
	for _, r := range repos {
		dest := repoDest(r)
		if r.Ref != "" {
			fmt.Fprintf(&b, "if [ -d %q/.git ]; then git -C %q fetch --all --prune && git -C %q checkout %q; "+
				"else git clone --branch %q %q %q; fi\n", dest, dest, dest, r.Ref, r.Ref, r.URL, dest)
		} else {
			fmt.Fprintf(&b, "if [ -d %q/.git ]; then git -C %q fetch --all --prune; "+
				"else git clone %q %q; fi\n", dest, dest, r.URL, dest)
		}
	}
	return b.String()
}

// agentDeployment builds the per-agent Deployment running the agent-runtime
// image under the bootstrap contract (v2 §4.1). Replicas are 0 under
// scale-to-zero (the Dispatcher scales up on demand), else 1. When the agent
// works on git repos, a `clone-repos` init-container materializes them into a
// shared workspace (the cluster equivalent of the local clone), authenticating
// as the team's git service account (docs/DEV-WORKFLOW.md §5b).
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
	secCtx := &corev1.SecurityContext{
		RunAsNonRoot:             &nonRoot,
		RunAsUser:                &runAsUser,
		ReadOnlyRootFilesystem:   &readOnly,
		AllowPrivilegeEscalation: &noPrivEsc,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	pullPolicy := agentPullPolicy(team)

	container := corev1.Container{
		Name:  "agent",
		Image: agentImage(team),
		Env:   agentEnv(team, agent),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "spec", MountPath: "/etc/karo", ReadOnly: true},
		},
		SecurityContext: secCtx,
	}
	if pullPolicy != "" {
		container.ImagePullPolicy = pullPolicy
	}

	volumes := []corev1.Volume{{
		Name: "spec",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: teamConfigMapName(team)},
			},
		},
	}}

	// Git working repos: clone the agent's repos into a shared workspace via an
	// init-container, and give the agent container that workspace, a writable HOME
	// (shared with the init-container so `git push` reuses its credentials), and
	// the git service-account env (docs/DEV-WORKFLOW.md §5b). Teams without repos
	// keep the original single-container pod shape unchanged.
	var initContainers []corev1.Container
	if repos := agentRepos(team, agent); len(repos) > 0 {
		gitSecret := gitSecretName(team)
		repoMounts := []corev1.VolumeMount{
			{Name: workspaceVolume, MountPath: workspaceMount},
			{Name: homeVolume, MountPath: homeMount},
		}
		volumes = append(volumes,
			corev1.Volume{Name: workspaceVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			corev1.Volume{Name: homeVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		)

		clone := corev1.Container{
			Name:            "clone-repos",
			Image:           agentImage(team),
			Command:         []string{"sh", "-c", cloneScript(repos, gitSecret != "")},
			EnvFrom:         gitEnvFrom(gitSecret),
			VolumeMounts:    repoMounts,
			SecurityContext: secCtx,
			WorkingDir:      workspaceMount,
		}
		if pullPolicy != "" {
			clone.ImagePullPolicy = pullPolicy
		}
		initContainers = append(initContainers, clone)

		container.VolumeMounts = append(container.VolumeMounts, repoMounts...)
		container.Env = append(container.Env, corev1.EnvVar{Name: "HOME", Value: homeMount})
		container.EnvFrom = append(container.EnvFrom, gitEnvFrom(gitSecret)...)
		container.WorkingDir = agentWorkingDir(repos)
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
					InitContainers: initContainers,
					Containers:     []corev1.Container{container},
					Volumes:        volumes,
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
