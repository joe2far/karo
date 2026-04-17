// Package v1alpha1: AgentGateway CRD
//
// AgentGateway declares a request-level proxy for agent traffic —
// agent→LLM, agent→tool (MCP), and agent→agent (A2A) — inspired by
// agentgateway.dev and the agentic-layer/agent-runtime-operator
// AgentGateway primitive. KARO manages the gateway as a first-class
// CRD so that governance (rate limits, budgets, auth), observability
// (unified metrics/tracing), and failover (provider fallback) live in
// the control plane alongside ModelConfig, ToolSet, and AgentSpec.
//
// Relationship to other CRDs:
//   - ModelConfig.spec.gatewayRef — when set, agents route LLM calls
//     through the referenced gateway instead of calling the provider
//     endpoint directly. The gateway controller resolves provider
//     credentials at runtime.
//   - ToolSet.spec.gatewayRef — when set, agents route MCP tool calls
//     through the referenced gateway. The gateway acts as an MCP
//     multiplexer and enforces per-tool permissions and rate limits.
//   - AgentSpec.spec.gatewayRef — a namespace-default gateway used when
//     neither ModelConfig nor ToolSet pin a gateway. ModelConfig and
//     ToolSet explicit refs win.
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentGatewaySpec defines the desired state of an AgentGateway proxy.
//
// +kubebuilder:validation:XValidation:rule="size(self.upstreams.models) + size(self.upstreams.tools) + size(self.upstreams.agents) > 0",message="at least one upstream (models, tools, or agents) must be declared"
type AgentGatewaySpec struct {
	// Image overrides the default agent gateway container image.
	// Defaults to the bundled agentgateway.dev build.
	Image string `json:"image,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8080
	ListenPort int32 `json:"listenPort,omitempty"`

	// Upstreams that this gateway proxies to.
	Upstreams GatewayUpstreams `json:"upstreams"`

	// Policy governs request-level enforcement (rate limits, budgets, auth).
	Policy GatewayPolicy `json:"policy,omitempty"`

	// Observability controls metrics and tracing exposure.
	Observability GatewayObservability `json:"observability,omitempty"`

	// Resources for the gateway pod.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// GatewayUpstreams groups the three traffic classes the gateway proxies.
// Each list is namespace-local — the gateway controller resolves
// ModelConfig, ToolSet, and peer AgentGateway references to their
// concrete endpoints on reconcile.
type GatewayUpstreams struct {
	// Models declares LLM upstreams. Each entry pins a ModelConfig the
	// gateway will route to; the gateway controller copies provider
	// credentials + endpoint from the ModelConfig into gateway config.
	Models []GatewayModelUpstream `json:"models,omitempty"`

	// Tools declares MCP tool upstreams. Each entry pins a ToolSet the
	// gateway will multiplex tool calls to.
	Tools []GatewayToolUpstream `json:"tools,omitempty"`

	// Agents declares A2A peer upstreams. v1alpha1 wires the config
	// plumbing; active A2A routing follows the A2A roadmap item.
	Agents []GatewayAgentUpstream `json:"agents,omitempty"`
}

type GatewayModelUpstream struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// ModelConfigRef pins a ModelConfig in the same namespace.
	ModelConfigRef corev1.LocalObjectReference `json:"modelConfigRef"`

	// +kubebuilder:validation:Minimum=0
	Weight int32 `json:"weight,omitempty"`

	// FallbackOrder lists other ModelConfig names (same namespace) to
	// fail over to when this upstream is unhealthy or over budget.
	FallbackOrder []string `json:"fallbackOrder,omitempty"`
}

type GatewayToolUpstream struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// ToolSetRef pins a ToolSet in the same namespace.
	ToolSetRef corev1.LocalObjectReference `json:"toolSetRef"`

	// AllowedTools optionally filters which tools from the ToolSet are
	// exposed through this gateway. Empty = all tools.
	AllowedTools []string `json:"allowedTools,omitempty"`
}

type GatewayAgentUpstream struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`

	// +kubebuilder:validation:Enum=a2a;mcp;http
	Protocol string `json:"protocol"`

	AuthSecret *corev1.SecretKeySelector `json:"authSecret,omitempty"`
}

// GatewayPolicy declares request-level enforcement applied by the gateway.
type GatewayPolicy struct {
	RateLimit *GatewayRateLimit `json:"rateLimit,omitempty"`
	Budget    *GatewayBudget    `json:"budget,omitempty"`
	Auth      *GatewayAuth      `json:"auth,omitempty"`
	// FailoverEnabled turns on provider-level failover using
	// GatewayModelUpstream.fallbackOrder.
	// +kubebuilder:default=true
	FailoverEnabled bool `json:"failoverEnabled,omitempty"`
}

type GatewayRateLimit struct {
	// +kubebuilder:validation:Minimum=0
	RequestsPerMinute int32 `json:"requestsPerMinute,omitempty"`
	// +kubebuilder:validation:Minimum=0
	TokensPerMinute int64 `json:"tokensPerMinute,omitempty"`
	// PerAgent applies the limit per AgentInstance caller rather than
	// globally across the gateway.
	// +kubebuilder:default=false
	PerAgent bool `json:"perAgent,omitempty"`
}

type GatewayBudget struct {
	// +kubebuilder:validation:Minimum=0
	DailyUSD float64 `json:"dailyUsd,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MonthlyUSD float64 `json:"monthlyUsd,omitempty"`
	// +kubebuilder:validation:Enum=block;warn;fallback
	// +kubebuilder:default=warn
	OnExceed string `json:"onExceed,omitempty"`
}

type GatewayAuth struct {
	// +kubebuilder:validation:Enum=mtls;bearer;none
	// +kubebuilder:default=none
	Mode          string                    `json:"mode,omitempty"`
	BearerSecret  *corev1.SecretKeySelector `json:"bearerSecret,omitempty"`
	TLSSecretName string                    `json:"tlsSecretName,omitempty"`
}

type GatewayObservability struct {
	// +kubebuilder:default=true
	MetricsEnabled bool `json:"metricsEnabled,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=9090
	MetricsPort int32 `json:"metricsPort,omitempty"`
	// +kubebuilder:default=false
	TracingEnabled bool `json:"tracingEnabled,omitempty"`
	// +kubebuilder:validation:Pattern=`^(https?://).+`
	TracingEndpoint string `json:"tracingEndpoint,omitempty"`
}

// AgentGatewayStatus reports observed state of the gateway deployment.
type AgentGatewayStatus struct {
	// +kubebuilder:validation:Enum=Pending;Ready;Degraded;Error
	Phase             string             `json:"phase,omitempty"`
	ResolvedEndpoint  string             `json:"resolvedEndpoint,omitempty"`
	ReadyReplicas     int32              `json:"readyReplicas,omitempty"`
	ResolvedUpstreams int32              `json:"resolvedUpstreams,omitempty"`
	LastReconciledAt  *metav1.Time       `json:"lastReconciledAt,omitempty"`
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.resolvedEndpoint`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// AgentGateway declares a request-level proxy for agent-to-LLM,
// agent-to-tool (MCP), and agent-to-agent (A2A) traffic.
type AgentGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentGatewaySpec   `json:"spec,omitempty"`
	Status            AgentGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentGateway{}, &AgentGatewayList{})
}
