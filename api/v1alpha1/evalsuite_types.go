package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type EvalSuiteSpec struct {
	AgentSpecRef corev1.LocalObjectReference `json:"agentSpecRef"`
	EvalCases    []EvalCase                  `json:"evalCases"`
	Schedule     string                      `json:"schedule,omitempty"`
}

type EvalCase struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Prompt      string      `json:"prompt,omitempty"`
	Assertions  []Assertion `json:"assertions"`
}

type Assertion struct {
	Type                AssertionType                `json:"type"`
	Value               string                       `json:"value,omitempty"`
	Pattern             string                       `json:"pattern,omitempty"`
	Criteria            string                       `json:"criteria,omitempty"`
	JudgeModelConfigRef *corev1.LocalObjectReference `json:"judgeModelConfigRef,omitempty"`
}

type AssertionType string

const (
	AssertionTypeContains          AssertionType = "contains"
	AssertionTypeNotContains       AssertionType = "not-contains"
	AssertionTypeMatchesPattern    AssertionType = "matches-pattern"
	AssertionTypeNotMatchesPattern AssertionType = "not-matches-pattern"
	AssertionTypeLLMJudge          AssertionType = "llm-judge"
)

type EvalSuiteStatus struct {
	Phase         string             `json:"phase,omitempty"`
	LastRunAt     *metav1.Time       `json:"lastRunAt,omitempty"`
	LastPassRate  float64            `json:"lastPassRate,omitempty"`
	LastRunResult string             `json:"lastRunResult,omitempty"`
	TotalCases    int32              `json:"totalCases,omitempty"`
	PassedCases   int32              `json:"passedCases,omitempty"`
	FailedCases   int32              `json:"failedCases,omitempty"`
	Conditions    []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cases",type=integer,JSONPath=`.status.totalCases`
// +kubebuilder:printcolumn:name="PassRate",type=number,JSONPath=`.status.lastPassRate`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type EvalSuite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              EvalSuiteSpec   `json:"spec,omitempty"`
	Status            EvalSuiteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type EvalSuiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvalSuite `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvalSuite{}, &EvalSuiteList{})
}
