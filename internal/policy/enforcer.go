// Package policy implements AgentPolicy enforcement.
//
// Architecture: Policy enforcement happens at two layers:
//
//  1. Admission-time (CEL validation on CRD types) — already implemented via
//     kubebuilder markers. Prevents invalid configurations from being created.
//
//  2. Runtime enforcement via policy ConfigMap injection:
//     - The AgentPolicy controller compiles policy rules into a JSON ConfigMap
//     - The AgentInstance controller mounts this ConfigMap into agent pods
//     - The agent-runtime-mcp sidecar reads the policy and enforces it at
//       tool-call time (rate limiting, model validation, data classification)
//
// This is the same pattern used by Kyverno (policy as data) and NeMo Guardrails
// (policy sidecar). No OPA/Gatekeeper dependency needed — the policy is simple
// enough to enforce inline in the MCP sidecar.
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

// CompiledPolicy is the JSON structure written into the policy ConfigMap.
// The agent-runtime-mcp sidecar deserializes this to enforce policy at runtime.
type CompiledPolicy struct {
	// Model constraints.
	AllowedProviders        []string `json:"allowedProviders,omitempty"`
	DeniedModels            []string `json:"deniedModels,omitempty"`
	RequireMinContextWindow int64    `json:"requireMinContextWindow,omitempty"`

	// Tool call governance.
	ToolCallMaxPerMinute     int32 `json:"toolCallMaxPerMinute,omitempty"`
	ToolCallMaxPerLoop       int32 `json:"toolCallMaxPerLoop,omitempty"`
	RequireSandboxForExecute bool  `json:"requireSandboxForExecute,omitempty"`

	// Loop governance.
	MaxIterationsPerRun                 int32 `json:"maxIterationsPerRun,omitempty"`
	MaxRunDurationMinutes               int32 `json:"maxRunDurationMinutes,omitempty"`
	RequireHumanApprovalAfterIterations int32 `json:"requireHumanApprovalAfterIterations,omitempty"`

	// TaskGraph mutation policy.
	AllowMutation     bool     `json:"allowMutation"`
	MutationScope     []string `json:"mutationScope,omitempty"`
	DenyMutation      []string `json:"denyMutation,omitempty"`
	RequireAuditTrail bool     `json:"requireAuditTrail,omitempty"`

	// Data classification.
	AllowedLevels       []string `json:"allowedLevels,omitempty"`
	DenyPatterns        []string `json:"denyPatterns,omitempty"`
	CompiledDenyRegexes []string `json:"-"` // Not serialized — compiled at load time.

	// Escalation.
	OnPolicyViolation string `json:"onPolicyViolation,omitempty"`

	// Audit.
	AuditEnabled  bool   `json:"auditEnabled"`
	AuditLogLevel string `json:"auditLogLevel,omitempty"`
}

// PolicyCompiler compiles AgentPolicy CRDs into ConfigMaps for runtime enforcement.
type PolicyCompiler struct {
	client client.Client
}

// NewPolicyCompiler creates a new PolicyCompiler.
func NewPolicyCompiler(c client.Client) *PolicyCompiler {
	return &PolicyCompiler{client: c}
}

// CompileAndPublish finds the AgentPolicy matching an AgentSpec's labels,
// compiles it into a ConfigMap, and creates/updates the ConfigMap in the namespace.
func (pc *PolicyCompiler) CompileAndPublish(ctx context.Context, agentSpec *karov1alpha1.AgentSpec) error {
	logger := log.FromContext(ctx)

	policy, err := pc.findMatchingPolicy(ctx, agentSpec)
	if err != nil {
		return err
	}
	if policy == nil {
		// No matching policy — no ConfigMap to create.
		return nil
	}

	compiled := pc.compile(policy)

	// Validate deny patterns are valid regexes.
	var validPatterns []string
	for _, p := range compiled.DenyPatterns {
		if _, err := regexp.Compile(p); err != nil {
			logger.Info("invalid deny pattern in policy, skipping", "pattern", p, "error", err)
			continue
		}
		validPatterns = append(validPatterns, p)
	}
	compiled.DenyPatterns = validPatterns

	return pc.ensureConfigMap(ctx, agentSpec, policy, compiled)
}

// GetPolicyConfigMapName returns the name of the policy ConfigMap for an AgentSpec.
func GetPolicyConfigMapName(agentSpecName string) string {
	return fmt.Sprintf("karo-policy-%s", agentSpecName)
}

// findMatchingPolicy finds the AgentPolicy whose targetSelector matches the AgentSpec's labels.
func (pc *PolicyCompiler) findMatchingPolicy(ctx context.Context, agentSpec *karov1alpha1.AgentSpec) (*karov1alpha1.AgentPolicy, error) {
	var policies karov1alpha1.AgentPolicyList
	if err := pc.client.List(ctx, &policies, client.InNamespace(agentSpec.Namespace)); err != nil {
		return nil, err
	}

	for i := range policies.Items {
		p := &policies.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(&p.Spec.TargetSelector)
		if err != nil {
			continue
		}
		if selector.Matches(labels.Set(agentSpec.Labels)) {
			return p, nil
		}
	}

	return nil, nil
}

// compile converts an AgentPolicy into a CompiledPolicy.
func (pc *PolicyCompiler) compile(policy *karov1alpha1.AgentPolicy) *CompiledPolicy {
	return &CompiledPolicy{
		AllowedProviders:        policy.Spec.Models.AllowedProviders,
		DeniedModels:            policy.Spec.Models.DeniedModels,
		RequireMinContextWindow: policy.Spec.Models.RequireMinContextWindow,

		ToolCallMaxPerMinute:     policy.Spec.ToolCalls.MaxPerMinute,
		ToolCallMaxPerLoop:       policy.Spec.ToolCalls.MaxPerLoop,
		RequireSandboxForExecute: policy.Spec.ToolCalls.RequireSandboxForExecute,

		MaxIterationsPerRun:                 policy.Spec.Loop.MaxIterationsPerRun,
		MaxRunDurationMinutes:               policy.Spec.Loop.MaxRunDurationMinutes,
		RequireHumanApprovalAfterIterations: policy.Spec.Loop.RequireHumanApprovalAfterIterations,

		AllowMutation:     policy.Spec.TaskGraph.AllowMutation,
		MutationScope:     policy.Spec.TaskGraph.MutationScope,
		DenyMutation:      policy.Spec.TaskGraph.DenyMutation,
		RequireAuditTrail: policy.Spec.TaskGraph.RequireAuditTrail,

		AllowedLevels: policy.Spec.DataClassification.AllowedLevels,
		DenyPatterns:  policy.Spec.DataClassification.DenyPatterns,

		OnPolicyViolation: policy.Spec.Escalation.OnPolicyViolation,

		AuditEnabled:  policy.Spec.Audit.Enabled,
		AuditLogLevel: policy.Spec.Audit.LogLevel,
	}
}

// ensureConfigMap creates or updates the policy ConfigMap.
func (pc *PolicyCompiler) ensureConfigMap(ctx context.Context, agentSpec *karov1alpha1.AgentSpec, policy *karov1alpha1.AgentPolicy, compiled *CompiledPolicy) error {
	cmName := GetPolicyConfigMapName(agentSpec.Name)

	policyJSON, err := json.MarshalIndent(compiled, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal compiled policy: %w", err)
	}

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: agentSpec.Namespace,
			Labels: map[string]string{
				"karo.dev/managed-by":   "karo-operator",
				"karo.dev/agent-spec":   agentSpec.Name,
				"karo.dev/agent-policy": policy.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "karo.dev/v1alpha1",
					Kind:       "AgentPolicy",
					Name:       policy.Name,
					UID:        policy.UID,
				},
			},
		},
		Data: map[string]string{
			"policy.json": string(policyJSON),
		},
	}

	var existing corev1.ConfigMap
	key := types.NamespacedName{Name: cmName, Namespace: agentSpec.Namespace}
	if err := pc.client.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return pc.client.Create(ctx, desired)
		}
		return err
	}

	existing.Data = desired.Data
	existing.Labels = desired.Labels
	return pc.client.Update(ctx, &existing)
}
