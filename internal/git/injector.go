package git

import (
	"fmt"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// BuildGitInitContainer creates the init container that configures git credentials.
func BuildGitInitContainer(agentSpec *karov1alpha1.AgentSpec, secrets map[string]string) corev1.Container {
	script := `#!/bin/sh
set -e
git config --global user.email "karo-agent@karo.dev"
git config --global user.name "KARO Agent"
`
	if agentSpec.Spec.WorkspaceCredentials != nil {
		for i, cred := range agentSpec.Spec.WorkspaceCredentials.Git {
			token := secrets[fmt.Sprintf("KARO_GIT_TOKEN_%d", i)]
			script += fmt.Sprintf(`
echo "https://oauth2:%s@%s" >> ~/.git-credentials
git config --global credential.helper store
`, token, cred.Host)
			_ = cred // suppress unused warning
		}
	}

	return corev1.Container{
		Name:    "git-credential-init",
		Image:   "ghcr.io/karo-dev/karo-git-init:latest",
		Command: []string{"sh", "-c", script},
		Env:     BuildGitEnvVars(agentSpec, secrets),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "home", MountPath: "/root"},
		},
	}
}

// BuildGitEnvVars creates environment variables for git credentials.
func BuildGitEnvVars(agentSpec *karov1alpha1.AgentSpec, secrets map[string]string) []corev1.EnvVar {
	if agentSpec.Spec.WorkspaceCredentials == nil {
		return nil
	}
	envVars := make([]corev1.EnvVar, 0, len(agentSpec.Spec.WorkspaceCredentials.Git)*3)
	for i, cred := range agentSpec.Spec.WorkspaceCredentials.Git {
		envVars = append(envVars,
			corev1.EnvVar{
				Name:  fmt.Sprintf("KARO_GIT_HOST_%d", i),
				Value: cred.Host,
			},
			corev1.EnvVar{
				Name:  fmt.Sprintf("KARO_GIT_TOKEN_%d", i),
				Value: secrets[fmt.Sprintf("KARO_GIT_TOKEN_%d", i)],
			},
			corev1.EnvVar{
				Name:  fmt.Sprintf("KARO_GIT_SCOPE_%d", i),
				Value: cred.Scope,
			},
		)
	}
	return envVars
}
