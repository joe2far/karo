package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentLoopSpec struct {
	AgentSpecRef      corev1.LocalObjectReference `json:"agentSpecRef"`
	Triggers          []LoopTrigger               `json:"triggers"`
	ContextCarryover  bool                        `json:"contextCarryover,omitempty"`
	LoopPrompt        *SystemPromptConfig         `json:"loopPrompt,omitempty"`
	MaxConcurrent     int32                       `json:"maxConcurrent,omitempty"`
	ConcurrencyPolicy string                      `json:"concurrencyPolicy,omitempty"`
	DispatcherRef     corev1.LocalObjectReference `json:"dispatcherRef"`
	EvalGate          *LoopEvalGate               `json:"evalGate,omitempty"`
}

type LoopTrigger struct {
	Type     string       `json:"type"`
	Schedule string       `json:"schedule,omitempty"`
	Source   *EventSource `json:"source,omitempty"`
}

type EventSource struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Event string `json:"event"`
}

type LoopEvalGate struct {
	EvalSuiteRef corev1.LocalObjectReference `json:"evalSuiteRef"`
	MinPassRate  float64                     `json:"minPassRate"`
}

type AgentLoopStatus struct {
	Phase               string             `json:"phase,omitempty"`
	LastRunAt           *metav1.Time       `json:"lastRunAt,omitempty"`
	NextRunAt           *metav1.Time       `json:"nextRunAt,omitempty"`
	LastRunResult       string             `json:"lastRunResult,omitempty"`
	ConsecutiveFailures int32              `json:"consecutiveFailures,omitempty"`
	Conditions          []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentSpecRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type AgentLoop struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentLoopSpec   `json:"spec,omitempty"`
	Status            AgentLoopStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentLoopList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentLoop `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentLoop{}, &AgentLoopList{})
}
