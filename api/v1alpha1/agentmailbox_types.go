package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type AgentMailboxSpec struct {
	AgentSpecRef         corev1.LocalObjectReference `json:"agentSpecRef"`
	AcceptedMessageTypes []MessageType               `json:"acceptedMessageTypes"`
	MaxPendingMessages   int32                       `json:"maxPendingMessages,omitempty"`
	MaxMessageSizeBytes  int32                       `json:"maxMessageSizeBytes,omitempty"`
	Delivery             DeliveryConfig              `json:"delivery"`
}

type DeliveryConfig struct {
	Type                   string `json:"type"`
	PollingIntervalSeconds int32  `json:"pollingIntervalSeconds,omitempty"`
}

type MessageType string

const (
	MessageTypeTaskAssigned     MessageType = "TaskAssigned"
	MessageTypeTaskDepUnblocked MessageType = "TaskDepUnblocked"
	MessageTypeHumanOverride    MessageType = "HumanOverride"
	MessageTypeLoopTick         MessageType = "LoopTick"
	MessageTypeEvalResult       MessageType = "EvalResult"
	MessageTypeAgentToAgent     MessageType = "AgentToAgent"
)

type AgentMailboxStatus struct {
	Phase                string             `json:"phase,omitempty"`
	PendingMessages      []MailboxMessage   `json:"pendingMessages,omitempty"`
	PendingCount         int32              `json:"pendingCount"`
	TotalReceived        int64              `json:"totalReceived"`
	TotalProcessed       int64              `json:"totalProcessed"`
	OldestPendingMessage *metav1.Time       `json:"oldestPendingMessage,omitempty"`
	Conditions           []metav1.Condition `json:"conditions,omitempty"`
}

type MailboxMessage struct {
	MessageType MessageType              `json:"messageType"`
	MessageID   string                   `json:"messageId"`
	Timestamp   metav1.Time              `json:"timestamp"`
	Payload     *runtime.RawExtension    `json:"payload"`
}

type TaskAssignedPayload struct {
	TaskGraphRef       corev1.LocalObjectReference `json:"taskGraphRef"`
	TaskID             string                      `json:"taskId"`
	TaskTitle          string                      `json:"taskTitle"`
	TaskType           TaskType                    `json:"taskType"`
	TaskDescription    string                      `json:"taskDescription,omitempty"`
	AcceptanceCriteria []string                    `json:"acceptanceCriteria,omitempty"`
	EvalGateEnabled    bool                        `json:"evalGateEnabled"`
	Priority           TaskPriority                `json:"priority"`
	PriorFailureNotes  string                      `json:"priorFailureNotes,omitempty"`
	SkillPrompt        string                      `json:"skillPrompt,omitempty"`
	ContextRefs        []ContextRef                `json:"contextRefs,omitempty"`
}

type ContextRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentSpecRef.name`
// +kubebuilder:printcolumn:name="Pending",type=integer,JSONPath=`.status.pendingCount`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type AgentMailbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentMailboxSpec   `json:"spec,omitempty"`
	Status            AgentMailboxStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentMailboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentMailbox `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentMailbox{}, &AgentMailboxList{})
}
