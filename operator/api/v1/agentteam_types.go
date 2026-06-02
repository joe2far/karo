package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ---------------------------------------------------------------------------
// Shared spec body — a faithful, typed mirror of PRD-KARO-CLI.md §4.2 and the
// karo-runtime JSON Schema (schema/agentteam.schema.json). Every field the
// compiled AgentTeam carries is represented here so `karo export` round-trips
// through `kubectl apply` without the apiserver pruning spec.resources /
// spec.memory / coordination.mailbox|taskLayer. Free-form sub-objects
// (model.params, tool.schema) use RawExtension + PreserveUnknownFields.
// The Go ⇄ JSON-Schema agreement is enforced by api/v1/schema_parity_test.go.
// ---------------------------------------------------------------------------

// ModelBinding selects the LLM backend for an agent (CLI §4.2).
type ModelBinding struct {
	// +kubebuilder:validation:Enum=anthropic;bedrock;vertex
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	// Profile is a LOCAL credential selection mechanism; ignored on cluster (§8).
	Profile string `json:"profile,omitempty"`
	// Params are free-form generation params (max_tokens, temperature, ...).
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Params *runtime.RawExtension `json:"params,omitempty"`
}

// McpServer declares an MCP server resource (CLI §4.2 / §9).
type McpServer struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Enum=stdio;http
	Transport string            `json:"transport,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// SkillRef references a Claude Code-style skill dir or marketplace pack.
type SkillRef struct {
	Source string `json:"source"`
}

// ToolDef declares a custom in-process tool (CLI §9).
type ToolDef struct {
	Name        string `json:"name"`
	Module      string `json:"module,omitempty"`
	Description string `json:"description,omitempty"`
	// Schema is a free-form JSON-schema-ish description of the tool input.
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Schema *runtime.RawExtension `json:"schema,omitempty"`
}

// Resources are the shared tools, skills and MCP servers (CLI §4.2 / §9).
type Resources struct {
	McpServers []McpServer `json:"mcpServers,omitempty"`
	Skills     []SkillRef  `json:"skills,omitempty"`
	Tools      []ToolDef   `json:"tools,omitempty"`
}

// Retention is the memory GC policy (CLI §10).
type Retention struct {
	MaxItems int `json:"maxItems,omitempty"`
	// +kubebuilder:validation:Enum=aggressive;lru;none
	GcStrategy string `json:"gcStrategy,omitempty"`
}

// Memory configures durable state. backend selects the LOCAL store; on cluster
// runtime.backends.memory overrides it (CLI §10, v2 §6).
type Memory struct {
	// +kubebuilder:validation:Enum=file;sqlite;redis;none
	Backend string `json:"backend,omitempty"`
	Path    string `json:"path,omitempty"`
	// +kubebuilder:validation:Enum=team;per-agent;both
	Scope     string     `json:"scope,omitempty"`
	Retention *Retention `json:"retention,omitempty"`
}

// MailboxConfig configures the durable mailbox (CLI §4.2 / §11).
type MailboxConfig struct {
	// +kubebuilder:validation:Enum=file;redis;none
	Backend   string `json:"backend,omitempty"`
	Path      string `json:"path,omitempty"`
	HardLimit int    `json:"hardLimit,omitempty"`
}

// TaskLayer configures the durable task store (CLI §4.2 / §11).
type TaskLayer struct {
	// +kubebuilder:validation:Enum=file;sqlite
	Backend   string `json:"backend,omitempty"`
	Path      string `json:"path,omitempty"`
	Resumable *bool  `json:"resumable,omitempty"`
}

// AgentBudget is an optional per-agent budget override (CLI §8).
type AgentBudget struct {
	Limit *int64 `json:"limit,omitempty"`
	// +kubebuilder:validation:Type=number
	Share *float64 `json:"share,omitempty"`
}

// AgentMemoryRef is a per-agent memory scope override (CLI §4.2).
type AgentMemoryRef struct {
	// +kubebuilder:validation:Enum=team;per-agent;both
	Scope string `json:"scope,omitempty"`
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
// +kubebuilder:validation:XValidation:rule="!(self.pattern == 'lead-and-teammates') || has(self.lead)",message="coordination.lead is required when pattern is 'lead-and-teammates'"
// +kubebuilder:validation:XValidation:rule="!(self.pattern == 'pipeline') || has(self.pipeline)",message="coordination.pipeline is required when pattern is 'pipeline'"
type Coordination struct {
	// +kubebuilder:validation:Enum=lead-and-teammates;pipeline;swarm
	Pattern   string         `json:"pattern,omitempty"`
	Lead      string         `json:"lead,omitempty"`
	Pipeline  *Pipeline      `json:"pipeline,omitempty"`
	Mailbox   *MailboxConfig `json:"mailbox,omitempty"`
	TaskLayer *TaskLayer     `json:"taskLayer,omitempty"`
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
	// +kubebuilder:validation:Enum=prompt;acceptEdits;plan;bypass
	PermissionMode string          `json:"permissionMode,omitempty"`
	Tools          []string        `json:"tools,omitempty"`
	MCP            []string        `json:"mcp,omitempty"`
	Skills         []string        `json:"skills,omitempty"`
	Mailbox        string          `json:"mailbox,omitempty"`
	Memory         *AgentMemoryRef `json:"memory,omitempty"`
	Budget         *AgentBudget    `json:"budget,omitempty"`
	Interaction    *Interaction    `json:"interaction,omitempty"`
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
	ScaleToZero         *bool                `json:"scaleToZero,omitempty"`
	IdleTimeoutSeconds  int                  `json:"idleTimeoutSeconds,omitempty"`
	MaxConcurrentAgents int                  `json:"maxConcurrentAgents,omitempty"`
	Backends            *Backends            `json:"backends,omitempty"`
	Observability       *Observability       `json:"observability,omitempty"`
	Image               *ImageSpec           `json:"image,omitempty"`
	Secrets             map[string]SecretRef `json:"secrets,omitempty"`
}

// AgentTeamSpec is the desired state: the shared compiled body + runtime block.
type AgentTeamSpec struct {
	Defaults     Defaults     `json:"defaults,omitempty"`
	Budgets      *Budgets     `json:"budgets,omitempty"`
	Resources    *Resources   `json:"resources,omitempty"`
	Memory       *Memory      `json:"memory,omitempty"`
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
