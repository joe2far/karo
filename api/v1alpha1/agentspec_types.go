package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule="self.scaling.maxInstances >= self.scaling.minInstances",message="maxInstances must be >= minInstances"
// +kubebuilder:validation:XValidation:rule="self.runtime.image != ''",message="runtime.image is required"
type AgentSpecSpec struct {
	ModelConfigRef       corev1.LocalObjectReference  `json:"modelConfigRef"`
	SystemPrompt         SystemPromptConfig           `json:"systemPrompt"`
	// +kubebuilder:validation:MinItems=1
	Capabilities         []AgentCapability            `json:"capabilities"`
	AgentConfigFiles     []AgentConfigFile            `json:"agentConfigFiles,omitempty"`
	MemoryRef            *corev1.LocalObjectReference `json:"memoryRef,omitempty"`
	ToolSetRef           *corev1.LocalObjectReference `json:"toolSetRef,omitempty"`
	SandboxClassRef      *corev1.LocalObjectReference `json:"sandboxClassRef,omitempty"`
	// GatewayRef is a namespace-default AgentGateway used for both LLM
	// and MCP traffic when the referenced ModelConfig/ToolSet do not
	// pin a gateway of their own. An explicit gatewayRef on a
	// ModelConfig or ToolSet takes precedence over this default.
	GatewayRef           *corev1.LocalObjectReference `json:"gatewayRef,omitempty"`
	WorkspaceCredentials *WorkspaceCredentialsConfig  `json:"workspaceCredentials,omitempty"`
	Runtime              RuntimeConfig                `json:"runtime"`
	// +kubebuilder:validation:Minimum=0
	MaxContextTokens     int64                        `json:"maxContextTokens,omitempty"`
	// +kubebuilder:validation:Enum=restart;checkpoint;terminate
	OnContextExhaustion  string                       `json:"onContextExhaustion,omitempty"`
	Dispatchable         bool                         `json:"dispatchable"`
	Scaling              AgentScaling                 `json:"scaling,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.maxInstances >= self.minInstances",message="maxInstances must be >= minInstances"
type AgentScaling struct {
	// +kubebuilder:validation:Minimum=0
	MinInstances    int32       `json:"minInstances"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	MaxInstances    int32       `json:"maxInstances"`
	// +kubebuilder:validation:Enum=OnDemand;Immediate
	// +kubebuilder:default=OnDemand
	StartPolicy     StartPolicy `json:"startPolicy,omitempty"`
	// +kubebuilder:validation:Minimum=0
	CooldownSeconds int32       `json:"cooldownSeconds,omitempty"`
}

type StartPolicy string

const (
	StartPolicyOnDemand  StartPolicy = "OnDemand"
	StartPolicyImmediate StartPolicy = "Immediate"
)

type AgentCapability struct {
	// +kubebuilder:validation:MinLength=1
	Name          string              `json:"name"`
	SkillPrompt   *SystemPromptConfig `json:"skillPrompt,omitempty"`
	RequiredTools []string            `json:"requiredTools,omitempty"`
}

type AgentConfigFile struct {
	// +kubebuilder:validation:MinLength=1
	Name      string           `json:"name"`
	// +kubebuilder:validation:MinLength=1
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
	// +kubebuilder:validation:MinLength=1
	Name             string                   `json:"name"`
	// +kubebuilder:validation:Enum=token;ssh
	Type             string                   `json:"type"`
	// +kubebuilder:validation:MinLength=1
	Host             string                   `json:"host"`
	CredentialSecret corev1.SecretKeySelector `json:"credentialSecret"`
	// +kubebuilder:validation:Enum=push;pull;read
	Scope            string                   `json:"scope"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.inline) && self.inline != '') || has(self.configMapRef)",message="either inline (non-empty) or configMapRef must be set"
type SystemPromptConfig struct {
	Inline       string           `json:"inline,omitempty"`
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

type ConfigMapKeyRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Key  string `json:"key"`
}

type RuntimeConfig struct {
	// +kubebuilder:validation:MinLength=1
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
