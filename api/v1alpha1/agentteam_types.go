package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentTeamSpec struct {
	Description string `json:"description,omitempty"`
	// +kubebuilder:validation:MinItems=1
	Agents          []AgentTeamMember            `json:"agents"`
	SharedResources TeamSharedResources          `json:"sharedResources,omitempty"`
	DispatcherRef   corev1.LocalObjectReference  `json:"dispatcherRef"`
	PolicyRef       *corev1.LocalObjectReference `json:"policyRef,omitempty"`
	LoopRef         *corev1.LocalObjectReference `json:"loopRef,omitempty"`
}

type AgentTeamMember struct {
	AgentSpecRef corev1.LocalObjectReference `json:"agentSpecRef"`
	// +kubebuilder:validation:Enum=orchestrator;executor;evaluator;reviewer
	Role string `json:"role"`
}

type TeamSharedResources struct {
	MemoryRef       *corev1.LocalObjectReference `json:"memoryRef,omitempty"`
	ToolSetRef      *corev1.LocalObjectReference `json:"toolSetRef,omitempty"`
	SandboxClassRef *corev1.LocalObjectReference `json:"sandboxClassRef,omitempty"`
	ModelConfigRef  *corev1.LocalObjectReference `json:"modelConfigRef,omitempty"`
}

type AgentTeamStatus struct {
	Phase           string             `json:"phase,omitempty"`
	ReadyAgents     int32              `json:"readyAgents,omitempty"`
	TotalAgents     int32              `json:"totalAgents,omitempty"`
	ActiveInstances int32              `json:"activeInstances,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyAgents`
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalAgents`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type AgentTeam struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentTeamSpec   `json:"spec,omitempty"`
	Status            AgentTeamStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentTeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTeam `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTeam{}, &AgentTeamList{})
}
