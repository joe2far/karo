package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Shared spec body (mirrors PRD-KARO-CLI.md §4.2). The CRD additionally sets
// x-kubernetes-preserve-unknown-fields on spec so the full schema validated by
// karo-runtime's JSON Schema round-trips even where the Go type is coarse-grained.
// ---------------------------------------------------------------------------

// ModelBinding selects the LLM backend for an agent (CLI §4.2).
type ModelBinding struct {
	// +kubebuilder:validation:Enum=anthropic;bedrock;vertex
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	// Profile is a LOCAL credential selection mechanism; ignored on cluster (§8).
	Profile string `json:"profile,omitempty"`
}

// Defaults are inherited by agents unless overridden (CLI §4.2).
type Defaults struct {
	// +kubebuilder:validation:Enum=sdk;claude-code;cursor;codex
	Harness string        `json:"harness,omitempty"`
	Model   *ModelBinding `json:"model,omitempty"`
	// +kubebuilder:validation:Enum=prompt;acceptEdits;plan;bypass
	PermissionMode string `json:"permissionMode,omitempty"`
	WorkingDir     string `json:"workingDir,omitempty"`
}

// TeamBudget is the team-wide token budget (CLI §8).
type TeamBudget struct {
	Provider string `json:"provider,omitempty"`
	// +kubebuilder:validation:Minimum=1
	Limit  int64  `json:"limit"`
	Window string `json:"window,omitempty"`
	// +kubebuilder:validation:Enum=warn;pause;hardstop
	OnExceed string `json:"onExceed,omitempty"`
}

// Budgets group team and per-agent budget policy.
type Budgets struct {
	Team     *TeamBudget `json:"team,omitempty"`
	PerAgent bool        `json:"perAgent,omitempty"`
}

// Pipeline declares the deterministic stage order for the pipeline pattern.
type Pipeline struct {
	Stages []string `json:"stages,omitempty"`
}

// Coordination is the team coordination policy (CLI §11).
type Coordination struct {
	// +kubebuilder:validation:Enum=lead-and-teammates;pipeline;swarm
	Pattern  string    `json:"pattern,omitempty"`
	Lead     string    `json:"lead,omitempty"`
	Pipeline *Pipeline `json:"pipeline,omitempty"`
}

// Guard is a pause-and-flag rule (CLI §13).
type Guard struct {
	PauseBefore []string `json:"pauseBefore,omitempty"`
	// +kubebuilder:validation:Enum=taskComplete;planReady;error
	PauseOn string `json:"pauseOn,omitempty"`
}

// Interaction is the attach & direct config (CLI §13).
type Interaction struct {
	Attachable *bool `json:"attachable,omitempty"`
	// +kubebuilder:validation:Enum=supervised;autonomous
	Autonomy     string  `json:"autonomy,omitempty"`
	Guards       []Guard `json:"guards,omitempty"`
	PauseTimeout int     `json:"pauseTimeout,omitempty"`
}

// Agent is a single role within the team (CLI §4.2).
type Agent struct {
	// +kubebuilder:validation:Required
	Name         string        `json:"name"`
	Instructions string        `json:"instructions,omitempty"`
	Harness      string        `json:"harness,omitempty"`
	Model        *ModelBinding `json:"model,omitempty"`
	Tools        []string      `json:"tools,omitempty"`
	MCP          []string      `json:"mcp,omitempty"`
	Skills       []string      `json:"skills,omitempty"`
	Mailbox      string        `json:"mailbox,omitempty"`
	Interaction  *Interaction  `json:"interaction,omitempty"`
}

// ---------------------------------------------------------------------------
// runtime: block (KARO v2 only, PRD-KARO-v2.md §3.1)
// ---------------------------------------------------------------------------

// SecretRef references a Secret name/key. Shared shape with `karo export` (§12).
type SecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

// Backend maps an abstract backend to a concrete cluster service (v2 §3.1).
type Backend struct {
	// +kubebuilder:validation:Enum=redis;postgres;sqlite;file
	Kind      string     `json:"kind"`
	SecretRef *SecretRef `json:"secretRef,omitempty"`
}

// Backends are the concrete coordination stores on cluster.
type Backends struct {
	Memory  *Backend `json:"memory,omitempty"`
	Mailbox *Backend `json:"mailbox,omitempty"`
	Tasks   *Backend `json:"tasks,omitempty"`
}

// TracingSpec configures OTel tracing export.
type TracingSpec struct {
	Exporter string `json:"exporter,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Sampling string `json:"sampling,omitempty"`
}

// Observability configures metrics + tracing (v2 §9).
type Observability struct {
	Metrics string       `json:"metrics,omitempty"`
	Tracing *TracingSpec `json:"tracing,omitempty"`
}

// ImageSpec is the agent-runtime image config.
type ImageSpec struct {
	AgentRuntime string `json:"agentRuntime,omitempty"`
	PullPolicy   string `json:"pullPolicy,omitempty"`
}

// RuntimeSpec is the KARO v2 runtime block (v2 §3.1).
type RuntimeSpec struct {
	ScaleToZero        *bool                `json:"scaleToZero,omitempty"`
	IdleTimeoutSeconds int                  `json:"idleTimeoutSeconds,omitempty"`
	MaxConcurrentAgents int                 `json:"maxConcurrentAgents,omitempty"`
	Backends           *Backends            `json:"backends,omitempty"`
	Observability      *Observability       `json:"observability,omitempty"`
	Image              *ImageSpec           `json:"image,omitempty"`
	Secrets            map[string]SecretRef `json:"secrets,omitempty"`
}

// AgentTeamSpec is the desired state: the shared compiled body + runtime block.
type AgentTeamSpec struct {
	Defaults     Defaults     `json:"defaults,omitempty"`
	Budgets      *Budgets     `json:"budgets,omitempty"`
	Coordination Coordination `json:"coordination,omitempty"`
	Interaction  Interaction  `json:"interaction,omitempty"`
	// +kubebuilder:validation:MinItems=1
	Agents  []Agent      `json:"agents"`
	Runtime *RuntimeSpec `json:"runtime,omitempty"`
}

// AgentTeamStatus is the observed state (v2 §3.1).
type AgentTeamStatus struct {
	// +kubebuilder:validation:Enum=Pending;Provisioning;Running;Idle;Degraded;Failed
	Phase              string             `json:"phase,omitempty"`
	ActiveAgents       int                `json:"activeAgents,omitempty"`
	PendingTasks       int                `json:"pendingTasks,omitempty"`
	Budget             *BudgetStatus      `json:"budget,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}

// BudgetStatus reflects the authoritative counter (v2 §5.6).
type BudgetStatus struct {
	Provider string `json:"provider,omitempty"`
	Used     int64  `json:"used,omitempty"`
	Limit    int64  `json:"limit,omitempty"`
	Window   string `json:"window,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=at
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Agents",type=integer,JSONPath=`.status.activeAgents`
// +kubebuilder:printcolumn:name="Pattern",type=string,JSONPath=`.spec.coordination.pattern`

// AgentTeam is the primary KARO custom resource.
type AgentTeam struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTeamSpec   `json:"spec,omitempty"`
	Status AgentTeamStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTeamList contains a list of AgentTeam.
type AgentTeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTeam `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTeam{}, &AgentTeamList{})
}
