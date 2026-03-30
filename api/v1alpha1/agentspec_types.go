package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentSpecSpec struct {
	ModelConfigRef       corev1.LocalObjectReference  `json:"modelConfigRef"`
	SystemPrompt         SystemPromptConfig           `json:"systemPrompt"`
	Capabilities         []AgentCapability            `json:"capabilities"`
	AgentConfigFiles     []AgentConfigFile            `json:"agentConfigFiles,omitempty"`
	MemoryRef            *corev1.LocalObjectReference `json:"memoryRef,omitempty"`
	ToolSetRef           *corev1.LocalObjectReference `json:"toolSetRef,omitempty"`
	SandboxClassRef      *corev1.LocalObjectReference `json:"sandboxClassRef,omitempty"`
	WorkspaceCredentials *WorkspaceCredentialsConfig  `json:"workspaceCredentials,omitempty"`
	Runtime              RuntimeConfig                `json:"runtime"`
	MaxContextTokens     int64                        `json:"maxContextTokens,omitempty"`
	OnContextExhaustion  string                       `json:"onContextExhaustion,omitempty"`
	Dispatchable         bool                         `json:"dispatchable"`
	Scaling              AgentScaling                 `json:"scaling,omitempty"`
}

type AgentScaling struct {
	MinInstances    int32       `json:"minInstances"`
	MaxInstances    int32       `json:"maxInstances"`
	StartPolicy     StartPolicy `json:"startPolicy,omitempty"`
	CooldownSeconds int32       `json:"cooldownSeconds,omitempty"`
}

type StartPolicy string

const (
	StartPolicyOnDemand  StartPolicy = "OnDemand"
	StartPolicyImmediate StartPolicy = "Immediate"
)

type AgentCapability struct {
	Name          string              `json:"name"`
	SkillPrompt   *SystemPromptConfig `json:"skillPrompt,omitempty"`
	RequiredTools []string            `json:"requiredTools,omitempty"`
}

type AgentConfigFile struct {
	Name      string           `json:"name"`
	MountPath string           `json:"mountPath"`
	Source    ConfigFileSource  `json:"source"`
}

type ConfigFileSource struct {
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

type WorkspaceCredentialsConfig struct {
	Git []GitCredential `json:"git,omitempty"`
}

type GitCredential struct {
	Name             string                   `json:"name"`
	Type             string                   `json:"type"`
	Host             string                   `json:"host"`
	CredentialSecret corev1.SecretKeySelector `json:"credentialSecret"`
	Scope            string                   `json:"scope"`
}

type SystemPromptConfig struct {
	Inline       string           `json:"inline,omitempty"`
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

type ConfigMapKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type RuntimeConfig struct {
	Image     string                       `json:"image"`
	Resources corev1.ResourceRequirements  `json:"resources,omitempty"`
}

type AgentSpecStatus struct {
	Phase                string             `json:"phase,omitempty"`
	ActiveInstances      int32              `json:"activeInstances,omitempty"`
	DesiredInstances     int32              `json:"desiredInstances,omitempty"`
	IdleInstances        int32              `json:"idleInstances,omitempty"`
	HibernatedInstances  int32              `json:"hibernatedInstances,omitempty"`
	LastUpdated          *metav1.Time       `json:"lastUpdated,omitempty"`
	Conditions           []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.activeInstances`
type AgentSpec struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentSpecSpec   `json:"spec,omitempty"`
	Status            AgentSpecStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentSpecList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSpec `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentSpec{}, &AgentSpecList{})
}
