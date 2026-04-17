// Package gateway translates KARO-native ModelConfig and ToolSet resources
// into the native agentgateway.dev data-plane resources (AgentgatewayBackend
// + Gateway API HTTPRoute) that actually drive a running gateway.
//
// Design decision (Option 2 — delegation):
//   - KARO does not deploy or own the gateway Pod. The user runs
//     agentgateway.dev as a Kubernetes Gateway API implementation and
//     declares a `Gateway` (gateway.networking.k8s.io/v1) resource with
//     `gatewayClassName: agentgateway`.
//   - KARO's job is to translate its declarative CRDs (ModelConfig / ToolSet)
//     into the native resources the gateway consumes:
//       * `AgentgatewayBackend` (agentgateway.dev/v1alpha1) — one per upstream
//       * `HTTPRoute` (gateway.networking.k8s.io/v1) — attaches the backend to
//         the user's Gateway at a KARO-owned path prefix
//   - We model these as `unstructured.Unstructured` so KARO does not take a
//     hard go.mod dependency on agentgateway's or gateway-api's Go types —
//     the translator is data-driven and version-tolerant.
//
// Credential wiring:
//   - anthropic / openai — rendered `spec.policies.auth.secretRef` points at
//     the ModelConfig's `apiKeySecret`. The gateway process reads the secret
//     at request time; KARO never proxies the credential.
//   - bedrock / vertexai — the gateway Pod's ServiceAccount must carry the
//     IRSA / Workload-Identity binding. KARO can't mutate a Gateway it does
//     not own, so we emit a status condition telling the operator which SA
//     annotations are required (from the ModelConfig's bedrock.irsaRoleArn or
//     vertex.gcpServiceAccount). The annotation applies to the Gateway's
//     backing Deployment ServiceAccount, not the KARO pod.
package gateway

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const (
	// AgentgatewayBackend GVK.
	agentgatewayGroup   = "agentgateway.dev"
	agentgatewayVersion = "v1alpha1"
	backendKind         = "AgentgatewayBackend"

	// Gateway API HTTPRoute GVK.
	gatewayAPIGroup   = "gateway.networking.k8s.io"
	gatewayAPIVersion = "v1"
	httpRouteKind     = "HTTPRoute"

	// KARO-owned label applied to every generated resource so we can scope
	// list/gc operations and humans can trace back to the owning KARO CR.
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelOwnerKind = "karo.dev/owner-kind"
	labelOwnerName = "karo.dev/owner-name"
	karoManager    = "karo-operator"

	// The ModelConfig controller uses this path prefix so clients can select
	// a specific backend via URL rather than header.
	ModelPathPrefix   = "/v1/models"
	ToolSetPathPrefix = "/v1/mcp"
)

// AgentgatewayBackendGVK returns the canonical GVK for the backend CR.
func AgentgatewayBackendGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: agentgatewayGroup, Version: agentgatewayVersion, Kind: backendKind}
}

// HTTPRouteGVK returns the canonical GVK for Gateway API HTTPRoute.
func HTTPRouteGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: gatewayAPIGroup, Version: gatewayAPIVersion, Kind: httpRouteKind}
}

// Translator is the ModelConfig/ToolSet → agentgateway.dev resource bridge.
//
// Takes a scheme so OwnerReference GVK resolution can round-trip typed
// objects via apiutil.GVKForObject (typed objects fetched by client.Get
// have an empty TypeMeta; we must resolve the GVK via the scheme).
type Translator struct {
	client client.Client
	scheme *runtime.Scheme
}

// NewTranslator builds a translator. The scheme must have the KARO v1alpha1
// types registered so OwnerReference GVK lookup works.
func NewTranslator(c client.Client, scheme *runtime.Scheme) *Translator {
	return &Translator{client: c, scheme: scheme}
}

// BackendNameForModel returns the AgentgatewayBackend object name used for a
// given ModelConfig. Deterministic so reconciliation is idempotent.
func BackendNameForModel(mc *karov1alpha1.ModelConfig) string {
	return fmt.Sprintf("karo-mc-%s", mc.Name)
}

// BackendNameForTool returns the AgentgatewayBackend object name used for a
// given ToolSet + tool entry.
func BackendNameForTool(ts *karov1alpha1.ToolSet, toolName string) string {
	return fmt.Sprintf("karo-ts-%s-%s", ts.Name, toolName)
}

// RouteNameForModel returns the HTTPRoute object name used for a ModelConfig.
func RouteNameForModel(mc *karov1alpha1.ModelConfig) string {
	return fmt.Sprintf("karo-mc-%s", mc.Name)
}

// RouteNameForToolSet returns the HTTPRoute object name used for a ToolSet.
func RouteNameForToolSet(ts *karov1alpha1.ToolSet) string {
	return fmt.Sprintf("karo-ts-%s", ts.Name)
}

// ModelPathFor returns the path prefix at which a given ModelConfig is
// exposed on the gateway. Agents dial {gateway}/{path} with an
// OpenAI-compatible payload.
func ModelPathFor(mc *karov1alpha1.ModelConfig) string {
	return fmt.Sprintf("%s/%s", ModelPathPrefix, mc.Name)
}

// ToolSetPathFor returns the path prefix at which a given ToolSet is
// exposed on the gateway.
func ToolSetPathFor(ts *karov1alpha1.ToolSet) string {
	return fmt.Sprintf("%s/%s", ToolSetPathPrefix, ts.Name)
}

// EnsureModelConfigResources creates or updates the AgentgatewayBackend
// + HTTPRoute for a ModelConfig. Returns the gateway-facing endpoint that
// callers should publish via ModelConfig.status.resolvedEndpoint.
func (t *Translator) EnsureModelConfigResources(ctx context.Context, mc *karov1alpha1.ModelConfig) (string, error) {
	if mc.Spec.GatewayRef == nil {
		return "", nil
	}
	backend, err := t.buildModelBackend(mc)
	if err != nil {
		return "", err
	}
	if err := t.applyOwned(ctx, backend, mc); err != nil {
		return "", fmt.Errorf("apply AgentgatewayBackend: %w", err)
	}
	route := t.buildRoute(
		RouteNameForModel(mc), mc.Namespace, mc.Spec.GatewayRef.Name,
		ModelPathFor(mc),
		BackendNameForModel(mc),
		ownerLabels("ModelConfig", mc.Name),
	)
	if err := t.applyOwned(ctx, route, mc); err != nil {
		return "", fmt.Errorf("apply HTTPRoute: %w", err)
	}
	return gatewayEndpoint(mc.Namespace, mc.Spec.GatewayRef.Name, ModelPathFor(mc)), nil
}

// EnsureToolSetResources creates or updates AgentgatewayBackend objects for
// each tool in the ToolSet plus a single HTTPRoute that fans out sub-paths
// to the right backends. After applying the desired set, stale backends
// for tools that were removed from ts.Spec.Tools are pruned by listing
// everything labelled as owned by this ToolSet and deleting anything not
// in the desired set.
//
// Returns the gateway-facing endpoint and a boolean indicating whether any
// routable tools were rendered (false = all tools were stdio and the caller
// should surface Degraded).
func (t *Translator) EnsureToolSetResources(ctx context.Context, ts *karov1alpha1.ToolSet) (string, bool, error) {
	if ts.Spec.GatewayRef == nil {
		return "", false, nil
	}

	desired := map[string]struct{}{}
	var skippedStdio []string
	for i := range ts.Spec.Tools {
		tool := &ts.Spec.Tools[i]
		if tool.Transport == karov1alpha1.MCPTransportStdio {
			skippedStdio = append(skippedStdio, tool.Name)
			continue
		}
		backend, err := t.buildToolBackend(ts, tool)
		if err != nil {
			return "", false, fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		if err := t.applyOwned(ctx, backend, ts); err != nil {
			return "", false, fmt.Errorf("tool %q: apply backend: %w", tool.Name, err)
		}
		desired[BackendNameForTool(ts, tool.Name)] = struct{}{}
	}

	if err := t.pruneOrphanToolBackends(ctx, ts, desired); err != nil {
		return "", false, fmt.Errorf("prune orphan backends: %w", err)
	}

	// If every tool in the set is stdio, we produce no HTTPRoute — an
	// empty rules list is rejected by the Gateway API validator. Clean up
	// any previous route and signal Degraded to the caller.
	if len(desired) == 0 {
		_ = t.deleteIgnoreNotFound(ctx, newEmpty(HTTPRouteGVK(), RouteNameForToolSet(ts), ts.Namespace))
		return "", false, fmt.Errorf("no routable tools in ToolSet (skipped stdio: %v)", skippedStdio)
	}

	route := t.buildToolSetRoute(ts)
	if err := t.applyOwned(ctx, route, ts); err != nil {
		return "", false, fmt.Errorf("apply HTTPRoute: %w", err)
	}
	return gatewayEndpoint(ts.Namespace, ts.Spec.GatewayRef.Name, ToolSetPathFor(ts)), true, nil
}

// pruneOrphanToolBackends lists every AgentgatewayBackend labelled as owned
// by this ToolSet and deletes any whose name is not in the desired set. Used
// to GC backends for tools that were removed from ts.Spec.Tools.
func (t *Translator) pruneOrphanToolBackends(ctx context.Context, ts *karov1alpha1.ToolSet, desired map[string]struct{}) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group: agentgatewayGroup, Version: agentgatewayVersion, Kind: backendKind + "List",
	})
	if err := t.client.List(ctx, list,
		client.InNamespace(ts.Namespace),
		client.MatchingLabels{
			labelOwnerKind: "toolset",
			labelOwnerName: ts.Name,
		}); err != nil {
		return err
	}
	for i := range list.Items {
		item := &list.Items[i]
		if _, keep := desired[item.GetName()]; keep {
			continue
		}
		if err := t.client.Delete(ctx, item); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete orphan %q: %w", item.GetName(), err)
		}
	}
	return nil
}

// CleanupModelConfigResources deletes the generated Backend + Route — used
// when GatewayRef is removed from a ModelConfig (ownerRefs handle the cascade
// when the ModelConfig itself is deleted, but not when the ref is cleared).
func (t *Translator) CleanupModelConfigResources(ctx context.Context, mc *karov1alpha1.ModelConfig) error {
	backend := newEmpty(AgentgatewayBackendGVK(), BackendNameForModel(mc), mc.Namespace)
	route := newEmpty(HTTPRouteGVK(), RouteNameForModel(mc), mc.Namespace)
	return t.deleteIgnoreNotFound(ctx, backend, route)
}

// CleanupToolSetResources deletes the generated per-tool Backends + Route.
func (t *Translator) CleanupToolSetResources(ctx context.Context, ts *karov1alpha1.ToolSet) error {
	objs := []*unstructured.Unstructured{
		newEmpty(HTTPRouteGVK(), RouteNameForToolSet(ts), ts.Namespace),
	}
	for i := range ts.Spec.Tools {
		objs = append(objs, newEmpty(AgentgatewayBackendGVK(), BackendNameForTool(ts, ts.Spec.Tools[i].Name), ts.Namespace))
	}
	return t.deleteIgnoreNotFound(ctx, objs...)
}

// RequiredServiceAccountAnnotations returns the workload-identity bindings
// the gateway's own Pod ServiceAccount must carry to authenticate as this
// ModelConfig's provider. Empty for providers that use static api keys.
func RequiredServiceAccountAnnotations(mc *karov1alpha1.ModelConfig) map[string]string {
	switch mc.Spec.Provider {
	case "bedrock":
		if mc.Spec.Bedrock == nil {
			return nil
		}
		return map[string]string{"eks.amazonaws.com/role-arn": mc.Spec.Bedrock.IRSARoleArn}
	case "vertex":
		if mc.Spec.Vertex == nil || mc.Spec.Vertex.GCPServiceAccount == "" {
			return nil
		}
		return map[string]string{"iam.gke.io/gcp-service-account": mc.Spec.Vertex.GCPServiceAccount}
	default:
		return nil
	}
}

// -----------------------------------------------------------------------------
// Backend builders
// -----------------------------------------------------------------------------

func (t *Translator) buildModelBackend(mc *karov1alpha1.ModelConfig) (*unstructured.Unstructured, error) {
	ai := map[string]interface{}{}
	switch mc.Spec.Provider {
	case "anthropic":
		provider := map[string]interface{}{"model": mc.Spec.Name}
		ai["provider"] = map[string]interface{}{"anthropic": provider}
	case "openai":
		provider := map[string]interface{}{"model": mc.Spec.Name}
		if mc.Spec.Endpoint != "" {
			provider["baseUrl"] = mc.Spec.Endpoint
		}
		ai["provider"] = map[string]interface{}{"openai": provider}
	case "bedrock":
		if mc.Spec.Bedrock == nil {
			return nil, fmt.Errorf("bedrock config missing on ModelConfig %s", mc.Name)
		}
		provider := map[string]interface{}{
			"model":  mc.Spec.Name,
			"region": mc.Spec.Bedrock.Region,
		}
		ai["provider"] = map[string]interface{}{"bedrock": provider}
	case "vertex":
		if mc.Spec.Vertex == nil {
			return nil, fmt.Errorf("vertex config missing on ModelConfig %s", mc.Name)
		}
		provider := map[string]interface{}{
			"model":   mc.Spec.Name,
			"project": mc.Spec.Vertex.Project,
		}
		if mc.Spec.Vertex.Location != "" {
			provider["location"] = mc.Spec.Vertex.Location
		}
		ai["provider"] = map[string]interface{}{"vertexai": provider}
	default:
		return nil, fmt.Errorf("unsupported provider %q for gateway translation", mc.Spec.Provider)
	}

	policies := map[string]interface{}{}
	// Static API key providers: wire the apiKeySecret into auth.secretRef so
	// the gateway reads the credential. The gateway reads the key directly
	// from the Secret; KARO never proxies it.
	if mc.Spec.APIKeySecret != nil && (mc.Spec.Provider == "anthropic" || mc.Spec.Provider == "openai") {
		policies["auth"] = map[string]interface{}{
			"secretRef": map[string]interface{}{
				"name": mc.Spec.APIKeySecret.Name,
			},
		}
	}

	spec := map[string]interface{}{"ai": ai}
	if len(policies) > 0 {
		spec["policies"] = policies
	}

	u := newEmpty(AgentgatewayBackendGVK(), BackendNameForModel(mc), mc.Namespace)
	u.Object["spec"] = spec
	u.SetLabels(ownerLabels("ModelConfig", mc.Name))
	return u, nil
}

func (t *Translator) buildToolBackend(ts *karov1alpha1.ToolSet, tool *karov1alpha1.ToolEntry) (*unstructured.Unstructured, error) {
	if tool.Transport == karov1alpha1.MCPTransportStdio {
		// stdio transport has no HTTP endpoint — skip backend creation for
		// this tool; the gateway cannot proxy stdio today. We return an
		// explicit error so the caller can report Degraded status.
		return nil, fmt.Errorf("tool %q uses stdio transport, which cannot be proxied by an HTTP gateway", tool.Name)
	}
	mcp := map[string]interface{}{
		"target":    tool.Endpoint,
		"transport": string(tool.Transport),
	}
	if len(tool.Permissions) > 0 {
		mcp["permissions"] = toInterfaceSlice(tool.Permissions)
	}
	spec := map[string]interface{}{
		"mcp": mcp,
	}
	if tool.CredentialSecret != nil {
		spec["policies"] = map[string]interface{}{
			"auth": map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name": tool.CredentialSecret.Name,
				},
			},
		}
	}
	u := newEmpty(AgentgatewayBackendGVK(), BackendNameForTool(ts, tool.Name), ts.Namespace)
	u.Object["spec"] = spec
	u.SetLabels(ownerLabels("ToolSet", ts.Name))
	return u, nil
}

// -----------------------------------------------------------------------------
// HTTPRoute builders
// -----------------------------------------------------------------------------

func (t *Translator) buildRoute(name, namespace, gatewayName, pathPrefix, backendName string, labels map[string]string) *unstructured.Unstructured {
	rule := map[string]interface{}{
		"matches": []interface{}{
			map[string]interface{}{
				"path": map[string]interface{}{
					"type":  "PathPrefix",
					"value": pathPrefix,
				},
			},
		},
		"backendRefs": []interface{}{
			map[string]interface{}{
				"group": agentgatewayGroup,
				"kind":  backendKind,
				"name":  backendName,
			},
		},
	}
	spec := map[string]interface{}{
		"parentRefs": []interface{}{
			map[string]interface{}{
				"group":     gatewayAPIGroup,
				"kind":      "Gateway",
				"name":      gatewayName,
				"namespace": namespace,
			},
		},
		"rules": []interface{}{rule},
	}
	u := newEmpty(HTTPRouteGVK(), name, namespace)
	u.Object["spec"] = spec
	u.SetLabels(labels)
	return u
}

// buildToolSetRoute emits one HTTPRoute with one rule per tool, each rule
// routing a sub-path under the ToolSet's prefix to that tool's backend.
func (t *Translator) buildToolSetRoute(ts *karov1alpha1.ToolSet) *unstructured.Unstructured {
	rules := make([]interface{}, 0, len(ts.Spec.Tools))
	for i := range ts.Spec.Tools {
		tool := &ts.Spec.Tools[i]
		if tool.Transport == karov1alpha1.MCPTransportStdio {
			continue
		}
		rules = append(rules, map[string]interface{}{
			"matches": []interface{}{
				map[string]interface{}{
					"path": map[string]interface{}{
						"type":  "PathPrefix",
						"value": fmt.Sprintf("%s/%s", ToolSetPathFor(ts), tool.Name),
					},
				},
			},
			"backendRefs": []interface{}{
				map[string]interface{}{
					"group": agentgatewayGroup,
					"kind":  backendKind,
					"name":  BackendNameForTool(ts, tool.Name),
				},
			},
		})
	}
	spec := map[string]interface{}{
		"parentRefs": []interface{}{
			map[string]interface{}{
				"group":     gatewayAPIGroup,
				"kind":      "Gateway",
				"name":      ts.Spec.GatewayRef.Name,
				"namespace": ts.Namespace,
			},
		},
		"rules": rules,
	}
	u := newEmpty(HTTPRouteGVK(), RouteNameForToolSet(ts), ts.Namespace)
	u.Object["spec"] = spec
	u.SetLabels(ownerLabels("ToolSet", ts.Name))
	return u
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// applyOwned upserts the given unstructured object with the supplied KARO
// owner attached. Uses a two-step get/create-or-update because server-side
// apply would require a field manager registration we don't need yet.
//
// The OwnerReference is set via controllerutil.SetControllerReference which
// resolves the owner's GVK through the scheme — typed objects fetched by
// client.Get have empty TypeMeta, so `owner.GetObjectKind()` alone returns
// an empty GVK and yields a broken OwnerReference.
func (t *Translator) applyOwned(ctx context.Context, desired *unstructured.Unstructured, owner client.Object) error {
	if err := controllerutil.SetControllerReference(owner, desired, t.scheme); err != nil {
		return fmt.Errorf("set owner reference: %w", err)
	}
	key := types.NamespacedName{Name: desired.GetName(), Namespace: desired.GetNamespace()}
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err := t.client.Get(ctx, key, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return t.client.Create(ctx, desired)
		}
		return err
	}
	existing.Object["spec"] = desired.Object["spec"]
	mergeLabels(existing, desired.GetLabels())
	// Re-assert the owner reference on the existing object too, in case it
	// was created before the scheme fix landed or got stripped by a user.
	if err := controllerutil.SetControllerReference(owner, existing, t.scheme); err != nil {
		return fmt.Errorf("set owner reference on existing: %w", err)
	}
	return t.client.Update(ctx, existing)
}

func (t *Translator) deleteIgnoreNotFound(ctx context.Context, objs ...*unstructured.Unstructured) error {
	for _, o := range objs {
		if err := t.client.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func newEmpty(gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	u.SetNamespace(namespace)
	return u
}

// ownerLabels returns the labels applied to every generated resource so
// downstream tooling can list-by-owner and we can prune orphans on tool
// removal. kind is lowercased for label-value compliance.
func ownerLabels(kind, name string) map[string]string {
	return map[string]string{
		labelManagedBy: karoManager,
		labelOwnerKind: strings.ToLower(kind),
		labelOwnerName: name,
	}
}

func mergeLabels(obj *unstructured.Unstructured, in map[string]string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range in {
		labels[k] = v
	}
	obj.SetLabels(labels)
}

func gatewayEndpoint(namespace, gatewayName, path string) string {
	return fmt.Sprintf("http://%s.%s.svc%s", gatewayName, namespace, path)
}

func toInterfaceSlice(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
