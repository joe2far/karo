package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// TestReconcileProvisionsScaleToZeroTeam is the M1 acceptance test (v2 §14):
// applying a scale-to-zero AgentTeam provisions a per-agent ConfigMap + a
// Deployment scaled to 0, and the team reports Pending (0 active agents).
func TestReconcileProvisionsScaleToZeroTeam(t *testing.T) {
	requireEnvtest(t)
	ctx := testContext()

	const ns = "default"
	yes := true
	at := &karov1.AgentTeam{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "karo.dev/v1",
			Kind:       "AgentTeam",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: ns},
		Spec: karov1.AgentTeamSpec{
			Coordination: karov1.Coordination{
				Pattern: "lead-and-teammates",
				Lead:    "lead",
			},
			Agents: []karov1.Agent{
				{Name: "lead", Harness: "sdk"},
			},
			Runtime: &karov1.RuntimeSpec{ScaleToZero: &yes},
		},
	}
	if err := testClient.Create(ctx, at); err != nil {
		t.Fatalf("create AgentTeam: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(ctx, at) })

	r := newReconciler()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "alpha"}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// status.phase == Pending (scale-to-zero → 0 active agents).
	var got karov1.AgentTeam
	if err := testClient.Get(ctx, req.NamespacedName, &got); err != nil {
		t.Fatalf("get reconciled team: %v", err)
	}
	if got.Status.Phase != "Pending" {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
	if got.Status.ActiveAgents != 0 {
		t.Fatalf("activeAgents = %d, want 0", got.Status.ActiveAgents)
	}

	// ConfigMap <team>-<agent>-spec exists.
	var cm corev1.ConfigMap
	cmKey := types.NamespacedName{Namespace: ns, Name: "alpha-lead-spec"}
	if err := testClient.Get(ctx, cmKey, &cm); err != nil {
		t.Fatalf("get agent ConfigMap: %v", err)
	}
	if cm.Data["team"] != "alpha" {
		t.Fatalf("configmap team = %q, want alpha", cm.Data["team"])
	}
	if _, ok := cm.Data["agent.json"]; !ok {
		t.Fatalf("configmap missing agent.json")
	}

	// Deployment <team>-<agent> exists with replicas 0.
	var dep appsv1.Deployment
	depKey := types.NamespacedName{Namespace: ns, Name: "alpha-lead"}
	if err := testClient.Get(ctx, depKey, &dep); err != nil {
		t.Fatalf("get agent Deployment: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("deployment replicas = %v, want 0", dep.Spec.Replicas)
	}
	if dep.Labels[labelTeam] != "alpha" || dep.Labels[labelAgent] != "lead" {
		t.Fatalf("deployment labels = %v, want team=alpha agent=lead", dep.Labels)
	}
}
