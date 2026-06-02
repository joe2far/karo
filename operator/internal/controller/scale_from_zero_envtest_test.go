package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// TestScaleFromZeroOnTask is the v2-M3 acceptance: a scale-to-zero team idles at
// 0 replicas, an AgentTask (e.g. from `karo sling --context`) wakes its owner
// *and* the lead, and a terminal task lets them scale back to zero (v2 §5.1).
func TestScaleFromZeroOnTask(t *testing.T) {
	requireEnvtest(t)
	ctx := testContext()
	const ns = "default"
	yes := true

	team := &karov1.AgentTeam{
		TypeMeta:   metav1.TypeMeta{APIVersion: "karo.dev/v1", Kind: "AgentTeam"},
		ObjectMeta: metav1.ObjectMeta{Name: "sfz", Namespace: ns},
		Spec: karov1.AgentTeamSpec{
			Coordination: karov1.Coordination{Pattern: "lead-and-teammates", Lead: "lead"},
			Agents: []karov1.Agent{
				{Name: "lead", Harness: "sdk"},
				{Name: "worker", Harness: "sdk"},
			},
			Runtime: &karov1.RuntimeSpec{ScaleToZero: &yes},
		},
	}
	if err := testClient.Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(ctx, team) })

	r := newReconciler()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "sfz"}}
	reconcile := func() {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	replicas := func(agent string) int32 {
		var dep appsv1.Deployment
		if err := testClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "sfz-" + agent}, &dep); err != nil {
			t.Fatalf("get deployment sfz-%s: %v", agent, err)
		}
		if dep.Spec.Replicas == nil {
			return -1
		}
		return *dep.Spec.Replicas
	}

	// 1. No tasks → everyone at 0.
	reconcile()
	if replicas("lead") != 0 || replicas("worker") != 0 {
		t.Fatalf("idle: lead=%d worker=%d, want 0/0", replicas("lead"), replicas("worker"))
	}

	// 2. A task owned by worker wakes worker AND the lead (which plans/claims).
	task := &karov1.AgentTask{
		TypeMeta:   metav1.TypeMeta{APIVersion: "karo.dev/v1", Kind: "AgentTask"},
		ObjectMeta: metav1.ObjectMeta{Name: "sfz-task-1", Namespace: ns},
		Spec:       karov1.AgentTaskSpec{Team: "sfz", Owner: "worker", Objective: "do it"},
	}
	if err := testClient.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(ctx, task) })

	reconcile()
	if replicas("worker") != 1 {
		t.Fatalf("after task: worker=%d, want 1", replicas("worker"))
	}
	if replicas("lead") != 1 {
		t.Fatalf("after task: lead=%d, want 1 (lead wakes when any work exists)", replicas("lead"))
	}

	// 3. Task reaches a terminal state → both scale back to zero.
	task.Status.State = "done"
	if err := testClient.Status().Update(ctx, task); err != nil {
		t.Fatalf("update task status: %v", err)
	}
	reconcile()
	if replicas("worker") != 0 || replicas("lead") != 0 {
		t.Fatalf("after done: lead=%d worker=%d, want 0/0", replicas("lead"), replicas("worker"))
	}
}
