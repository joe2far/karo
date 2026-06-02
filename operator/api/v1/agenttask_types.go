package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentTaskSpec is one durable task (PRD-KARO-v2.md §3.2). The persisted source
// of truth is the tasks backend; AgentTask is the K8s-visible projection.
type AgentTaskSpec struct {
	Team               string   `json:"team"`
	Owner              string   `json:"owner,omitempty"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptanceCriteria,omitempty"`
	// DependsOn is the runtime projection of coordination.pipeline.stages (§6).
	DependsOn []string `json:"dependsOn,omitempty"`
	Guard     *Guard   `json:"guard,omitempty"`
}

// AgentTaskStatus mirrors the task lifecycle (CLI §11, v2 §3.2).
type AgentTaskStatus struct {
	// +kubebuilder:validation:Enum=pending;assigned;in-progress;blocked;review;paused;done;failed;cancelled
	State          string       `json:"state,omitempty"`
	Attempts       int          `json:"attempts,omitempty"`
	Owner          string       `json:"owner,omitempty"`
	Lease          *metav1.Time `json:"lease,omitempty"`
	AttachedBy     string       `json:"attachedBy,omitempty"`
	LastTransition *metav1.Time `json:"lastTransition,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.status.owner`
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=`.spec.team`

// AgentTask is a durable task projection.
type AgentTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTaskSpec   `json:"spec,omitempty"`
	Status AgentTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTaskList contains a list of AgentTask.
type AgentTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTask `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTask{}, &AgentTaskList{})
}
