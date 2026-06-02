package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// repoTeam builds a team whose agents work on git repos, with an optional
// git-credentials Secret reference in runtime.secrets["git"].
func repoTeam(gitSecret string, repos []karov1.Repo, agents ...karov1.Agent) *karov1.AgentTeam {
	at := team(agents...)
	at.Spec.Resources = &karov1.Resources{Repos: repos}
	if gitSecret != "" {
		at.Spec.Runtime = &karov1.RuntimeSpec{
			Secrets: map[string]karov1.SecretRef{gitSecretKey: {Name: gitSecret}},
		}
	}
	return at
}

// TestDeploymentNoReposIsUnchanged guards backward compatibility: a team with no
// repos keeps the original single-container, single-volume pod shape (no
// init-container, no workspace/home volumes).
func TestDeploymentNoReposIsUnchanged(t *testing.T) {
	at := team(karov1.Agent{Name: "solo", Harness: "sdk"})
	pod := agentDeployment(at, at.Spec.Agents[0]).Spec.Template.Spec

	if len(pod.InitContainers) != 0 {
		t.Fatalf("no-repo agent should have no init-containers, got %d", len(pod.InitContainers))
	}
	if len(pod.Volumes) != 1 || pod.Volumes[0].Name != "spec" {
		t.Fatalf("no-repo agent should have only the spec volume, got %v", pod.Volumes)
	}
	if c := pod.Containers[0]; c.WorkingDir != "" || len(c.EnvFrom) != 0 {
		t.Fatalf("no-repo agent container should have no workingDir/envFrom, got wd=%q envFrom=%v", c.WorkingDir, c.EnvFrom)
	}
}

// TestDeploymentClonesAgentRepos checks the clone init-container, the shared
// workspace/home volumes, and the single-repo working dir (CLI §3e parity).
func TestDeploymentClonesAgentRepos(t *testing.T) {
	repos := []karov1.Repo{
		{Name: "app", URL: "https://github.com/acme/app.git", Ref: "main"},
		{Name: "other", URL: "https://github.com/acme/other.git"},
	}
	// Agent references only "app".
	at := repoTeam("", repos, karov1.Agent{Name: "dev", Harness: "sdk", Repos: []string{"app"}})
	pod := agentDeployment(at, at.Spec.Agents[0]).Spec.Template.Spec

	if len(pod.InitContainers) != 1 || pod.InitContainers[0].Name != "clone-repos" {
		t.Fatalf("expected a clone-repos init-container, got %v", pod.InitContainers)
	}
	script := strings.Join(pod.InitContainers[0].Command, " ")
	if !strings.Contains(script, "https://github.com/acme/app.git") {
		t.Fatalf("clone script should clone the referenced repo, got:\n%s", script)
	}
	if strings.Contains(script, "acme/other.git") {
		t.Fatalf("clone script should NOT clone repos the agent doesn't reference, got:\n%s", script)
	}
	if !strings.Contains(script, `--branch "main"`) {
		t.Fatalf("clone script should honor the repo ref, got:\n%s", script)
	}

	if !hasVolume(pod.Volumes, workspaceVolume) || !hasVolume(pod.Volumes, homeVolume) {
		t.Fatalf("expected workspace + home volumes, got %v", pod.Volumes)
	}
	c := pod.Containers[0]
	if c.WorkingDir != workspaceMount+"/app" {
		t.Fatalf("single-repo agent should work inside its repo, got workingDir=%q", c.WorkingDir)
	}
	if !hasEnv(c.Env, "HOME", homeMount) {
		t.Fatalf("agent container should set HOME=%s, got %v", homeMount, c.Env)
	}
	if !hasMount(c.VolumeMounts, workspaceVolume) || !hasMount(c.VolumeMounts, homeVolume) {
		t.Fatalf("agent container should mount workspace + home, got %v", c.VolumeMounts)
	}
}

// TestDeploymentMultiRepoWorkingDir: an agent with several repos runs in the
// workspace root, not inside any one repo; a path override controls the dest.
func TestDeploymentMultiRepoWorkingDir(t *testing.T) {
	repos := []karov1.Repo{
		{Name: "api", URL: "https://github.com/acme/api.git"},
		{Name: "web", URL: "https://github.com/acme/web.git", Path: "frontend"},
	}
	at := repoTeam("", repos, karov1.Agent{Name: "dev", Harness: "sdk", Repos: []string{"api", "web"}})
	pod := agentDeployment(at, at.Spec.Agents[0]).Spec.Template.Spec

	if c := pod.Containers[0]; c.WorkingDir != workspaceMount {
		t.Fatalf("multi-repo agent should run in the workspace root, got %q", c.WorkingDir)
	}
	if script := strings.Join(pod.InitContainers[0].Command, " "); !strings.Contains(script, workspaceMount+"/frontend") {
		t.Fatalf("clone dest should honor the repo path override, got:\n%s", script)
	}
}

// TestDeploymentWiresGitServiceAccount: with runtime.secrets["git"] set, the
// credential is exposed to both the init-container (clone) and the agent
// container (push), and the clone script installs the token credential helper.
func TestDeploymentWiresGitServiceAccount(t *testing.T) {
	repos := []karov1.Repo{{Name: "app", URL: "https://github.com/acme/app.git"}}
	at := repoTeam("git-credentials", repos, karov1.Agent{Name: "dev", Harness: "sdk", Repos: []string{"app"}})
	pod := agentDeployment(at, at.Spec.Agents[0]).Spec.Template.Spec

	if !envFromSecret(pod.InitContainers[0].EnvFrom, "git-credentials") {
		t.Fatalf("init-container should source the git secret, got %v", pod.InitContainers[0].EnvFrom)
	}
	if !envFromSecret(pod.Containers[0].EnvFrom, "git-credentials") {
		t.Fatalf("agent container should source the git secret (for push), got %v", pod.Containers[0].EnvFrom)
	}
	script := strings.Join(pod.InitContainers[0].Command, " ")
	if !strings.Contains(script, "credential.helper") || !strings.Contains(script, "GITHUB_TOKEN") {
		t.Fatalf("clone script should install a token credential helper, got:\n%s", script)
	}
}

// TestDeploymentPublicReposNeedNoSecret: repos without a git secret still clone
// (unauthenticated), with no envFrom wired anywhere.
func TestDeploymentPublicReposNeedNoSecret(t *testing.T) {
	repos := []karov1.Repo{{Name: "app", URL: "https://github.com/public/app.git"}}
	at := repoTeam("", repos, karov1.Agent{Name: "dev", Harness: "sdk", Repos: []string{"app"}})
	pod := agentDeployment(at, at.Spec.Agents[0]).Spec.Template.Spec

	if len(pod.InitContainers[0].EnvFrom) != 0 || len(pod.Containers[0].EnvFrom) != 0 {
		t.Fatal("no git secret → no envFrom should be wired")
	}
	if script := strings.Join(pod.InitContainers[0].Command, " "); strings.Contains(script, "credential.helper") {
		t.Fatalf("no secret → no credential helper, got:\n%s", script)
	}
}

func TestGitSecretName(t *testing.T) {
	if n := gitSecretName(team(karov1.Agent{Name: "a"})); n != "" {
		t.Fatalf("no runtime → empty git secret, got %q", n)
	}
	at := repoTeam("creds", nil, karov1.Agent{Name: "a"})
	if n := gitSecretName(at); n != "creds" {
		t.Fatalf("expected runtime.secrets[git].name, got %q", n)
	}
}

// --- small assertion helpers ---

func hasVolume(vols []corev1.Volume, name string) bool {
	for _, v := range vols {
		if v.Name == name {
			return true
		}
	}
	return false
}

func hasMount(mounts []corev1.VolumeMount, name string) bool {
	for _, m := range mounts {
		if m.Name == name {
			return true
		}
	}
	return false
}

func hasEnv(env []corev1.EnvVar, name, value string) bool {
	for _, e := range env {
		if e.Name == name && e.Value == value {
			return true
		}
	}
	return false
}

func envFromSecret(sources []corev1.EnvFromSource, name string) bool {
	for _, s := range sources {
		if s.SecretRef != nil && s.SecretRef.Name == name {
			return true
		}
	}
	return false
}
