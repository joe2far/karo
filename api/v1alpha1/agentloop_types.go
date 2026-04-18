package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentLoopSpec struct {
	AgentSpecRef corev1.LocalObjectReference `json:"agentSpecRef"`
	// +kubebuilder:validation:MinItems=1
	Triggers         []LoopTrigger       `json:"triggers"`
	ContextCarryover bool                `json:"contextCarryover,omitempty"`
	LoopPrompt       *SystemPromptConfig `json:"loopPrompt,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MaxConcurrent int32 `json:"maxConcurrent,omitempty"`
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	ConcurrencyPolicy string                      `json:"concurrencyPolicy,omitempty"`
	DispatcherRef     corev1.LocalObjectReference `json:"dispatcherRef"`
	EvalGate          *LoopEvalGate               `json:"evalGate,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.type != 'cron' || self.schedule != ”",message="schedule required for cron triggers"
// +kubebuilder:validation:XValidation:rule="self.type != 'event' || has(self.source)",message="source required for event triggers"
type LoopTrigger struct {
	// +kubebuilder:validation:Enum=cron;event;webhook
	Type     string       `json:"type"`
	Schedule string       `json:"schedule,omitempty"`
	Source   *EventSource `json:"source,omitempty"`
}

type EventSource struct {
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Event string `json:"event"`
}

// +kubebuilder:validation:XValidation:rule="self.minPassRate >= 0.0 && self.minPassRate <= 1.0",message="minPassRate must be between 0.0 and 1.0"
type LoopEvalGate struct {
	EvalSuiteRef corev1.LocalObjectReference `json:"evalSuiteRef"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	MinPassRate float64 `json:"minPassRate"`
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
