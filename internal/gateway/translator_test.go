package gateway

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const (
	testNS      = "team-alpha"
	testGateway = "alpha-agent-gateway"
)

func newTestTranslator(t *testing.T, initial ...client.Object) *Translator {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := karov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initial...).
		Build()
	return NewTranslator(c, scheme)
}

// modelConfig builds a minimal ModelConfig with a UID so OwnerReference
// resolution works against the fake client.
func modelConfig(name, provider, modelName string, mutate func(*karov1alpha1.ModelConfig)) *karov1alpha1.ModelConfig {
	mc := &karov1alpha1.ModelConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			UID:       types.UID("uid-" + name),
		},
		Spec: karov1alpha1.ModelConfigSpec{
			Provider:   provider,
			Name:       modelName,
			GatewayRef: &corev1.LocalObjectReference{Name: testGateway},
		},
	}
	if mutate != nil {
		mutate(mc)
	}
	return mc
}

func TestEnsureModelConfigResources_AnthropicWiresAuthSecretRef(t *testing.T) {
	mc := modelConfig("claude", "anthropic", "claude-sonnet-4-20250514", func(m *karov1alpha1.ModelConfig) {
		m.Spec.APIKeySecret = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "anthropic-creds"},
			Key:                  "ANTHROPIC_API_KEY",
		}
	})
	tr := newTestTranslator(t, mc)

	endpoint, err := tr.EnsureModelConfigResources(context.Background(), mc)
	if err != nil {
		t.Fatalf("EnsureModelConfigResources: %v", err)
	}
	wantEndpoint := "http://alpha-agent-gateway.team-alpha.svc/v1/models/claude"
	if endpoint != wantEndpoint {
		t.Errorf("endpoint: got %q want %q", endpoint, wantEndpoint)
	}

	backend := getUnstructured(t, tr, AgentgatewayBackendGVK(), BackendNameForModel(mc), testNS)
	provider, _, _ := unstructured.NestedMap(backend.Object, "spec", "ai", "provider", "anthropic")
	if provider["model"] != "claude-sonnet-4-20250514" {
		t.Errorf("anthropic model: got %v", provider["model"])
	}
	authSecret, _, _ := unstructured.NestedString(backend.Object, "spec", "policies", "auth", "secretRef", "name")
	if authSecret != "anthropic-creds" {
		t.Errorf("auth secretRef.name: got %q want anthropic-creds", authSecret)
	}

	// OwnerReference must have a non-empty Kind/APIVersion — regression
	// test for the typed-object-empty-TypeMeta bug.
	refs := backend.GetOwnerReferences()
	if len(refs) != 1 {
		t.Fatalf("ownerRefs: got %d want 1", len(refs))
	}
	if refs[0].Kind != "ModelConfig" || refs[0].APIVersion != "karo.dev/v1alpha1" {
		t.Errorf("ownerRef GVK: got %s/%s", refs[0].APIVersion, refs[0].Kind)
	}

	// HTTPRoute attaches to the right Gateway at the right path prefix.
	route := getUnstructured(t, tr, HTTPRouteGVK(), RouteNameForModel(mc), testNS)
	parents, _, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	if len(parents) != 1 {
		t.Fatalf("parentRefs: got %d", len(parents))
	}
	parent := parents[0].(map[string]interface{})
	if parent["name"] != testGateway {
		t.Errorf("parentRef name: got %v want %s", parent["name"], testGateway)
	}
}

func TestEnsureModelConfigResources_Bedrock(t *testing.T) {
	mc := modelConfig("bedrock-claude", "bedrock", "anthropic.claude-3-5-sonnet", func(m *karov1alpha1.ModelConfig) {
		m.Spec.Bedrock = &karov1alpha1.BedrockConfig{
			Region:      "us-east-1",
			IRSARoleArn: "arn:aws:iam::123:role/karo-bedrock",
		}
	})
	tr := newTestTranslator(t, mc)
	if _, err := tr.EnsureModelConfigResources(context.Background(), mc); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	backend := getUnstructured(t, tr, AgentgatewayBackendGVK(), BackendNameForModel(mc), testNS)
	provider, _, _ := unstructured.NestedMap(backend.Object, "spec", "ai", "provider", "bedrock")
	if provider["region"] != "us-east-1" {
		t.Errorf("bedrock region: got %v", provider["region"])
	}
	// Bedrock must NOT get auth.secretRef (it uses IRSA on the gateway SA).
	if _, found, _ := unstructured.NestedMap(backend.Object, "spec", "policies", "auth"); found {
		t.Errorf("bedrock backend unexpectedly carries policies.auth")
	}

	// Required SA annotation exposed via helper.
	annots := RequiredServiceAccountAnnotations(mc)
	if annots["eks.amazonaws.com/role-arn"] != mc.Spec.Bedrock.IRSARoleArn {
		t.Errorf("IRSA annotation missing: %v", annots)
	}
}

func TestEnsureModelConfigResources_Vertex(t *testing.T) {
	mc := modelConfig("gemini", "vertex", "gemini-1.5-pro", func(m *karov1alpha1.ModelConfig) {
		m.Spec.Vertex = &karov1alpha1.VertexConfig{
			Project:           "my-gcp-project",
			Location:          "us-central1",
			GCPServiceAccount: "karo-vertex@my-gcp-project.iam.gserviceaccount.com",
		}
	})
	tr := newTestTranslator(t, mc)
	if _, err := tr.EnsureModelConfigResources(context.Background(), mc); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	backend := getUnstructured(t, tr, AgentgatewayBackendGVK(), BackendNameForModel(mc), testNS)
	provider, _, _ := unstructured.NestedMap(backend.Object, "spec", "ai", "provider", "vertexai")
	if provider["project"] != "my-gcp-project" {
		t.Errorf("vertex project: got %v", provider["project"])
	}
	if provider["location"] != "us-central1" {
		t.Errorf("vertex location: got %v", provider["location"])
	}
	annots := RequiredServiceAccountAnnotations(mc)
	if annots["iam.gke.io/gcp-service-account"] != mc.Spec.Vertex.GCPServiceAccount {
		t.Errorf("vertex WI annotation missing: %v", annots)
	}
}

func TestEnsureModelConfigResources_NoGatewayRef(t *testing.T) {
	mc := modelConfig("claude", "anthropic", "claude", nil)
	mc.Spec.GatewayRef = nil
	tr := newTestTranslator(t, mc)

	endpoint, err := tr.EnsureModelConfigResources(context.Background(), mc)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if endpoint != "" {
		t.Errorf("endpoint: got %q want empty", endpoint)
	}
}

func TestEnsureToolSetResources_PrunesOrphans(t *testing.T) {
	ts := &karov1alpha1.ToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "tools", Namespace: testNS, UID: "uid-tools"},
		Spec: karov1alpha1.ToolSetSpec{
			GatewayRef: &corev1.LocalObjectReference{Name: testGateway},
			Tools: []karov1alpha1.ToolEntry{
				{Name: "github", Type: "mcp", Transport: karov1alpha1.MCPTransportStreamableHTTP, Endpoint: "http://gh:8080"},
				{Name: "web-search", Type: "mcp", Transport: karov1alpha1.MCPTransportSSE, Endpoint: "http://ws:8080"},
			},
		},
	}

	// Seed an orphan backend that looks like it came from a previous
	// ToolSet revision (tool "old-tool" has been removed from spec).
	orphan := &unstructured.Unstructured{}
	orphan.SetGroupVersionKind(AgentgatewayBackendGVK())
	orphan.SetName(BackendNameForTool(ts, "old-tool"))
	orphan.SetNamespace(testNS)
	orphan.SetLabels(map[string]string{
		labelManagedBy: karoManager,
		labelOwnerKind: "toolset",
		labelOwnerName: ts.Name,
	})

	tr := newTestTranslator(t, ts, orphan)
	endpoint, routable, err := tr.EnsureToolSetResources(context.Background(), ts)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !routable {
		t.Fatalf("expected routable=true")
	}
	if endpoint == "" {
		t.Errorf("endpoint empty")
	}

	// Orphan must be gone.
	if _, err := getUnstructuredOrErr(tr, AgentgatewayBackendGVK(), orphan.GetName(), testNS); err == nil {
		t.Errorf("orphan backend %q was not pruned", orphan.GetName())
	}

	// Desired backends exist.
	for _, tool := range ts.Spec.Tools {
		getUnstructured(t, tr, AgentgatewayBackendGVK(), BackendNameForTool(ts, tool.Name), testNS)
	}
}

func TestEnsureToolSetResources_AllStdioReturnsDegraded(t *testing.T) {
	ts := &karov1alpha1.ToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "cli", Namespace: testNS, UID: "uid-cli"},
		Spec: karov1alpha1.ToolSetSpec{
			GatewayRef: &corev1.LocalObjectReference{Name: testGateway},
			Tools: []karov1alpha1.ToolEntry{
				{Name: "code-exec", Type: "mcp", Transport: karov1alpha1.MCPTransportStdio, Command: []string{"/bin/exec"}},
			},
		},
	}
	tr := newTestTranslator(t, ts)
	_, routable, err := tr.EnsureToolSetResources(context.Background(), ts)
	if err == nil {
		t.Errorf("expected error for all-stdio ToolSet")
	}
	if routable {
		t.Errorf("routable must be false when all tools are stdio")
	}
}

func TestCleanupModelConfigResources_RemovesBackendAndRoute(t *testing.T) {
	mc := modelConfig("claude", "anthropic", "claude", func(m *karov1alpha1.ModelConfig) {
		m.Spec.APIKeySecret = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "anthropic-creds"},
			Key:                  "ANTHROPIC_API_KEY",
		}
	})
	tr := newTestTranslator(t, mc)
	if _, err := tr.EnsureModelConfigResources(context.Background(), mc); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if err := tr.CleanupModelConfigResources(context.Background(), mc); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := getUnstructuredOrErr(tr, AgentgatewayBackendGVK(), BackendNameForModel(mc), testNS); err == nil {
		t.Error("backend was not deleted")
	}
	if _, err := getUnstructuredOrErr(tr, HTTPRouteGVK(), RouteNameForModel(mc), testNS); err == nil {
		t.Error("route was not deleted")
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func getUnstructured(t *testing.T, tr *Translator, gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	t.Helper()
	obj, err := getUnstructuredOrErr(tr, gvk, name, namespace)
	if err != nil {
		t.Fatalf("get %s/%s: %v", gvk.Kind, name, err)
	}
	return obj
}

func getUnstructuredOrErr(tr *Translator, gvk schema.GroupVersionKind, name, namespace string) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	err := tr.client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, u)
	return u, err
}
