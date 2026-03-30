package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentPolicySpec struct {
	TargetSelector     metav1.LabelSelector    `json:"targetSelector"`
	Models             ModelConstraints         `json:"models,omitempty"`
	ToolCalls          ToolCallGovernance       `json:"toolCalls,omitempty"`
	Loop               LoopGovernance           `json:"loop,omitempty"`
	TaskGraph          TaskGraphMutationPolicy  `json:"taskGraph,omitempty"`
	Audit              AuditConfig              `json:"audit,omitempty"`
	DataClassification DataClassificationConfig `json:"dataClassification,omitempty"`
	Escalation         EscalationConfig         `json:"escalation,omitempty"`
}

type ModelConstraints struct {
	AllowedProviders        []string `json:"allowedProviders,omitempty"`
	DeniedModels            []string `json:"deniedModels,omitempty"`
	RequireMinContextWindow int64    `json:"requireMinContextWindow,omitempty"`
}

type ToolCallGovernance struct {
	MaxPerMinute             int32 `json:"maxPerMinute,omitempty"`
	MaxPerLoop               int32 `json:"maxPerLoop,omitempty"`
	RequireSandboxForExecute bool  `json:"requireSandboxForExecute,omitempty"`
}

type LoopGovernance struct {
	MaxIterationsPerRun                 int32 `json:"maxIterationsPerRun,omitempty"`
	MaxRunDurationMinutes               int32 `json:"maxRunDurationMinutes,omitempty"`
	RequireHumanApprovalAfterIterations int32 `json:"requireHumanApprovalAfterIterations,omitempty"`
}

type TaskGraphMutationPolicy struct {
	AllowMutation     bool     `json:"allowMutation"`
	MutationScope     []string `json:"mutationScope,omitempty"`
	DenyMutation      []string `json:"denyMutation,omitempty"`
	RequireAuditTrail bool     `json:"requireAuditTrail,omitempty"`
}

type AuditConfig struct {
	Enabled        bool                 `json:"enabled"`
	LogLevel       string               `json:"logLevel"`
	LogDestination LogDestinationConfig `json:"logDestination,omitempty"`
	RetentionDays  int32                `json:"retentionDays,omitempty"`
}

type LogDestinationConfig struct {
	Type string `json:"type"`
}

type DataClassificationConfig struct {
	AllowedLevels []string `json:"allowedLevels,omitempty"`
	DenyPatterns  []string `json:"denyPatterns,omitempty"`
}

type EscalationConfig struct {
	OnPolicyViolation string `json:"onPolicyViolation"`
	NotifyWebhook     string `json:"notifyWebhook,omitempty"`
}

type AgentPolicyStatus struct {
	Phase             string             `json:"phase,omitempty"`
	ViolationsLast24h int32              `json:"violationsLast24h,omitempty"`
	LastEvaluatedAt   *metav1.Time       `json:"lastEvaluatedAt,omitempty"`
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type AgentPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentPolicySpec   `json:"spec,omitempty"`
	Status            AgentPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPolicy{}, &AgentPolicyList{})
}
