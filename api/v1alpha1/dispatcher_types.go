package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule="self.mode != 'capability' || size(self.capabilityRoutes) > 0",message="capabilityRoutes required when mode is capability"
// +kubebuilder:validation:XValidation:rule="self.mode != 'llm-route' || has(self.llmRoute)",message="llmRoute config required when mode is llm-route"
type DispatcherSpec struct {
	// +kubebuilder:validation:Enum=capability;llm-route;round-robin
	Mode                 DispatchMode                 `json:"mode"`
	TaskGraphSelector    metav1.LabelSelector         `json:"taskGraphSelector,omitempty"`
	CapabilityRoutes     []CapabilityRoute            `json:"capabilityRoutes,omitempty"`
	FallbackAgentSpecRef *corev1.LocalObjectReference `json:"fallbackAgentSpecRef,omitempty"`
	Messaging            MessagingConfig              `json:"messaging"`
	LLMRoute             *LLMRouteConfig              `json:"llmRoute,omitempty"`
}

type DispatchMode string

const (
	DispatchModeCapability DispatchMode = "capability"
	DispatchModeLLMRoute   DispatchMode = "llm-route"
	DispatchModeRoundRobin DispatchMode = "round-robin"
)

type CapabilityRoute struct {
	// +kubebuilder:validation:MinLength=1
	Capability   string                      `json:"capability"`
	AgentSpecRef corev1.LocalObjectReference `json:"agentSpecRef"`
}

type MessagingConfig struct {
	// +kubebuilder:validation:Enum=mailbox;webhook;kafka
	Type           string `json:"type"`
	MailboxPattern string `json:"mailboxPattern,omitempty"`
}

type LLMRouteConfig struct {
	ModelConfigRef   corev1.LocalObjectReference `json:"modelConfigRef"`
	RoutingPromptRef *SystemPromptConfig         `json:"routingPromptRef,omitempty"`
}

type DispatcherStatus struct {
	Phase            string             `json:"phase,omitempty"`
	TotalDispatched  int64              `json:"totalDispatched,omitempty"`
	PendingTasks     int32              `json:"pendingTasks,omitempty"`
	LastDispatchedAt *metav1.Time       `json:"lastDispatchedAt,omitempty"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Dispatched",type=integer,JSONPath=`.status.totalDispatched`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type Dispatcher struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DispatcherSpec   `json:"spec,omitempty"`
	Status            DispatcherStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DispatcherList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Dispatcher `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Dispatcher{}, &DispatcherList{})
}
