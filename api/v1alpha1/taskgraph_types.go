package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TaskGraphSpec defines the desired state of TaskGraph
type TaskGraphSpec struct {
	Description    string                      `json:"description,omitempty"`
	OwnerAgentRef  corev1.LocalObjectReference `json:"ownerAgentRef"`
	DispatcherRef  corev1.LocalObjectReference `json:"dispatcherRef"`
	Tasks          []Task                      `json:"tasks"`
	DispatchPolicy DispatchPolicy              `json:"dispatchPolicy"`
}

type Task struct {
	ID                   string                       `json:"id"`
	Title                string                       `json:"title"`
	Type                 TaskType                     `json:"type"`
	Description          string                       `json:"description,omitempty"`
	Deps                 []string                     `json:"deps"`
	Priority             TaskPriority                 `json:"priority"`
	AddedBy              string                       `json:"addedBy"`
	AddedAt              metav1.Time                  `json:"addedAt"`
	TimeoutMinutes       *int32                       `json:"timeoutMinutes,omitempty"`
	AcceptanceCriteria   []string                     `json:"acceptanceCriteria,omitempty"`
	EvalGate             *EvalGate                    `json:"evalGate,omitempty"`
	SandboxClassOverride *corev1.LocalObjectReference `json:"sandboxClassOverride,omitempty"`
}

type TaskType string

const (
	TaskTypeDesign   TaskType = "design"
	TaskTypeImpl     TaskType = "impl"
	TaskTypeEval     TaskType = "eval"
	TaskTypeReview   TaskType = "review"
	TaskTypeInfra    TaskType = "infra"
	TaskTypeApproval TaskType = "approval"
)

type TaskPriority string

const (
	TaskPriorityHigh   TaskPriority = "High"
	TaskPriorityMedium TaskPriority = "Medium"
	TaskPriorityLow    TaskPriority = "Low"
)

type EvalGate struct {
	EvalSuiteRef corev1.LocalObjectReference `json:"evalSuiteRef"`
	MinPassRate  float64                     `json:"minPassRate"`
	OnFail       EvalGateFailAction          `json:"onFail"`
}

type EvalGateFailAction string

const (
	EvalGateFailReopen   EvalGateFailAction = "Reopen"
	EvalGateFailEscalate EvalGateFailAction = "Escalate"
)

type DispatchPolicy struct {
	MaxConcurrent         int32       `json:"maxConcurrent"`
	DefaultTimeoutMinutes *int32      `json:"defaultTimeoutMinutes,omitempty"`
	RetryPolicy           RetryPolicy `json:"retryPolicy"`
	AllowAgentMutation    bool        `json:"allowAgentMutation"`
}

type RetryPolicy struct {
	MaxRetries     int32  `json:"maxRetries"`
	BackoffSeconds int32  `json:"backoffSeconds"`
	OnExhaustion   string `json:"onExhaustion"`
}

// TaskGraphStatus defines the observed state of TaskGraph
type TaskGraphStatus struct {
	Phase             TaskGraphPhase              `json:"phase"`
	TotalTasks        int32                       `json:"totalTasks"`
	OpenTasks         int32                       `json:"openTasks"`
	DispatchedTasks   int32                       `json:"dispatchedTasks"`
	InProgressTasks   int32                       `json:"inProgressTasks"`
	EvalPendingTasks  int32                       `json:"evalPendingTasks"`
	ClosedTasks       int32                       `json:"closedTasks"`
	FailedTasks       int32                       `json:"failedTasks"`
	BlockedTasks      int32                       `json:"blockedTasks"`
	CompletionPercent int32                       `json:"completionPercent"`
	LastDispatchedAt  *metav1.Time                `json:"lastDispatchedAt,omitempty"`
	TaskStatuses      map[string]TaskRuntimeState `json:"taskStatuses,omitempty"`
	Conditions        []metav1.Condition          `json:"conditions,omitempty"`
}

type TaskGraphPhase string

const (
	TaskGraphPhasePending    TaskGraphPhase = "Pending"
	TaskGraphPhaseInProgress TaskGraphPhase = "InProgress"
	TaskGraphPhaseCompleted  TaskGraphPhase = "Completed"
	TaskGraphPhaseFailed     TaskGraphPhase = "Failed"
	TaskGraphPhaseBlocked    TaskGraphPhase = "Blocked"
)

type TaskRuntimeState struct {
	Phase             TaskPhase    `json:"phase"`
	AssignedTo        string       `json:"assignedTo,omitempty"`
	AssignedAt        *metav1.Time `json:"assignedAt,omitempty"`
	StartedAt         *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt       *metav1.Time `json:"completedAt,omitempty"`
	RetryCount        int32        `json:"retryCount"`
	ResultArtifactRef string       `json:"resultArtifactRef,omitempty"`
	FailureNotes      string       `json:"failureNotes,omitempty"`
	EvalResult        *EvalResult  `json:"evalResult,omitempty"`
}

type TaskPhase string

const (
	TaskPhaseOpen             TaskPhase = "Open"
	TaskPhaseDispatched       TaskPhase = "Dispatched"
	TaskPhaseInProgress       TaskPhase = "InProgress"
	TaskPhaseEvalPending      TaskPhase = "EvalPending"
	TaskPhaseAwaitingApproval TaskPhase = "AwaitingApproval"
	TaskPhaseClosed           TaskPhase = "Closed"
	TaskPhaseFailed           TaskPhase = "Failed"
	TaskPhaseBlocked          TaskPhase = "Blocked"
)

type EvalResult struct {
	PassRate     float64     `json:"passRate"`
	Passed       bool        `json:"passed"`
	FailureNotes string      `json:"failureNotes,omitempty"`
	EvaluatedAt  metav1.Time `json:"evaluatedAt"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Tasks",type=integer,JSONPath=`.status.totalTasks`
// +kubebuilder:printcolumn:name="Completed",type=integer,JSONPath=`.status.closedTasks`
// +kubebuilder:printcolumn:name="Completion%",type=integer,JSONPath=`.status.completionPercent`
type TaskGraph struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TaskGraphSpec   `json:"spec,omitempty"`
	Status            TaskGraphStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type TaskGraphList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskGraph `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TaskGraph{}, &TaskGraphList{})
}
