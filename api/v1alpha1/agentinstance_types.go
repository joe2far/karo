package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentInstanceSpec struct {
	AgentSpecRef   corev1.LocalObjectReference `json:"agentSpecRef"`
	CurrentTaskRef *TaskRef                    `json:"currentTaskRef,omitempty"`
	Hibernation    HibernationConfig           `json:"hibernation,omitempty"`
}

type TaskRef struct {
	TaskGraph string `json:"taskGraph"`
	TaskID    string `json:"taskId"`
}

type HibernationConfig struct {
	IdleAfter    metav1.Duration `json:"idleAfter,omitempty"`
	ResumeOnMail bool            `json:"resumeOnMail,omitempty"`
}

type AgentInstanceStatus struct {
	Phase             AgentInstancePhase      `json:"phase"`
	PodRef            *corev1.ObjectReference `json:"podRef,omitempty"`
	StartedAt         *metav1.Time            `json:"startedAt,omitempty"`
	LastActiveAt      *metav1.Time            `json:"lastActiveAt,omitempty"`
	ContextTokensUsed int64                   `json:"contextTokensUsed"`
	Conditions        []metav1.Condition      `json:"conditions,omitempty"`
}

type AgentInstancePhase string

const (
	AgentInstancePhasePending    AgentInstancePhase = "Pending"
	AgentInstancePhaseRunning    AgentInstancePhase = "Running"
	AgentInstancePhaseIdle       AgentInstancePhase = "Idle"
	AgentInstancePhaseHibernated AgentInstancePhase = "Hibernated"
	AgentInstancePhaseTerminated AgentInstancePhase = "Terminated"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentSpecRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type AgentInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentInstanceSpec   `json:"spec,omitempty"`
	Status            AgentInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentInstance{}, &AgentInstanceList{})
}
