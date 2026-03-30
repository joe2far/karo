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
	// +kubebuilder:validation:MinItems=1
	Tasks          []Task                      `json:"tasks"`
	DispatchPolicy DispatchPolicy              `json:"dispatchPolicy"`
}

// +kubebuilder:validation:XValidation:rule="self.id != ''",message="task id must not be empty"
// +kubebuilder:validation:XValidation:rule="self.title != ''",message="task title must not be empty"
// +kubebuilder:validation:XValidation:rule="!has(self.evalGate) || self.evalGate.minPassRate >= 0.0 && self.evalGate.minPassRate <= 1.0",message="evalGate.minPassRate must be between 0.0 and 1.0"
// +kubebuilder:validation:XValidation:rule="!has(self.timeoutMinutes) || self.timeoutMinutes > 0",message="timeoutMinutes must be positive"
// +kubebuilder:validation:XValidation:rule="!self.deps.exists(d, d == self.id)",message="task cannot depend on itself"
type Task struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`
	ID                   string                       `json:"id"`
	// +kubebuilder:validation:MinLength=1
	Title                string                       `json:"title"`
	// +kubebuilder:validation:Enum=design;impl;eval;review;infra;approval
	Type                 TaskType                     `json:"type"`
	Description          string                       `json:"description,omitempty"`
	Deps                 []string                     `json:"deps"`
	// +kubebuilder:validation:Enum=High;Medium;Low
	Priority             TaskPriority                 `json:"priority"`
	AddedBy              string                       `json:"addedBy"`
	AddedAt              metav1.Time                  `json:"addedAt"`
	// +kubebuilder:validation:Minimum=1
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

// +kubebuilder:validation:XValidation:rule="self.minPassRate >= 0.0 && self.minPassRate <= 1.0",message="minPassRate must be between 0.0 and 1.0"
type EvalGate struct {
	EvalSuiteRef corev1.LocalObjectReference `json:"evalSuiteRef"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	MinPassRate  float64                     `json:"minPassRate"`
	// +kubebuilder:validation:Enum=Reopen;Escalate
	OnFail       EvalGateFailAction          `json:"onFail"`
}

type EvalGateFailAction string

const (
	EvalGateFailReopen   EvalGateFailAction = "Reopen"
	EvalGateFailEscalate EvalGateFailAction = "Escalate"
)

// +kubebuilder:validation:XValidation:rule="self.maxConcurrent >= 0",message="maxConcurrent must be non-negative"
// +kubebuilder:validation:XValidation:rule="self.retryPolicy.maxRetries >= 0",message="maxRetries must be non-negative"
type DispatchPolicy struct {
	// +kubebuilder:validation:Minimum=0
	MaxConcurrent         int32       `json:"maxConcurrent"`
	// +kubebuilder:validation:Minimum=1
	DefaultTimeoutMinutes *int32      `json:"defaultTimeoutMinutes,omitempty"`
	RetryPolicy           RetryPolicy `json:"retryPolicy"`
	AllowAgentMutation    bool        `json:"allowAgentMutation"`
}

type RetryPolicy struct {
	// +kubebuilder:validation:Minimum=0
	MaxRetries     int32  `json:"maxRetries"`
	// +kubebuilder:validation:Minimum=0
	BackoffSeconds int32  `json:"backoffSeconds"`
	// +kubebuilder:validation:Enum=EscalateToHuman;Fail;Reopen
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
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
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
