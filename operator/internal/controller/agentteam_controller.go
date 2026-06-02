package controller

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// clusterCapableHarnesses mirrors karo-runtime's set (CLI §4.7). Only these can
// run as headless agent pods.
var clusterCapableHarnesses = []string{"sdk"}

// AgentTeamReconciler reconciles an AgentTeam into running workloads.
type AgentTeamReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=karo.dev,resources=agentteams,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentteams/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentteams/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements the reconciliation loop (PRD-KARO-v2.md §5).
func (r *AgentTeamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var team karov1.AgentTeam
	if err := r.Get(ctx, req.NamespacedName, &team); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1. Validate: accepted apiVersion + cluster-capable harnesses (§5 step 1).
	if reason := validateTeam(&team); reason != "" {
		logger.Info("validation failed", "reason", reason)
		r.setCondition(&team, "Ready", metav1.ConditionFalse, "ValidationFailed", reason)
		team.Status.Phase = "Failed"
		return ctrl.Result{}, r.Status().Update(ctx, &team)
	}

	// 2. Ensure backends (placeholder: real impl verifies Redis/Postgres §5.2).
	r.setCondition(&team, "BackendsReady", metav1.ConditionTrue, "Assumed", "backends assumed reachable")

	// 3-4. Provision agents: create-or-update the per-agent ConfigMap + Deployment
	//      (scale-to-zero → 0 replicas until a task arrives). ActiveAgents is the
	//      observed count of deployments with a ready replica (§5.4).
	active, err := r.provisionAgents(ctx, &team)
	if err != nil {
		logger.Error(err, "provisioning agents failed")
		r.setCondition(&team, "Ready", metav1.ConditionFalse, "ProvisionFailed", err.Error())
		team.Status.Phase = "Degraded"
		if uerr := r.Status().Update(ctx, &team); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{}, err
	}
	team.Status.ActiveAgents = active

	// 6. Budget status reflects the authoritative counter (status only; §5.6).
	if team.Spec.Budgets != nil && team.Spec.Budgets.Team != nil {
		tb := team.Spec.Budgets.Team
		team.Status.Budget = &karov1.BudgetStatus{
			Provider: tb.Provider, Limit: tb.Limit, Window: tb.Window,
		}
	}

	// 7. Update status. Until agents are actually provisioned the team is
	//    Pending (the M0 acceptance bar: apply sample → Pending); once the
	//    Dispatcher has live agent pods it is Running (v2 §3.1 phase set).
	team.Status.Phase = phaseFor(team.Status.ActiveAgents)
	team.Status.ObservedGeneration = team.Generation
	r.setCondition(&team, "Ready", metav1.ConditionTrue, "Reconciled", "team reconciled")
	if err := r.Status().Update(ctx, &team); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func validateTeam(team *karov1.AgentTeam) string {
	if team.APIVersion != "" && !slices.Contains(karov1.AcceptedAPIVersions, team.APIVersion) {
		return fmt.Sprintf("unsupported apiVersion %q; this release accepts only %v", team.APIVersion, karov1.AcceptedAPIVersions)
	}
	if len(team.Spec.Agents) == 0 {
		return "spec.agents must contain at least one agent"
	}
	// Cross-field rules (CLI §4.3), mirrored from karo-runtime's validate.py so the
	// operator agrees with `karo validate`. The CRD's XValidation enforces the
	// coordination shape rules too; these give actionable messages + cover the
	// prompt+autonomous rule which spans defaults/interaction/agents.
	pattern := team.Spec.Coordination.Pattern
	if pattern == "" {
		pattern = "lead-and-teammates"
	}
	switch pattern {
	case "lead-and-teammates":
		if team.Spec.Coordination.Lead == "" {
			return "coordination.lead is required when pattern is 'lead-and-teammates'"
		}
	case "pipeline":
		if team.Spec.Coordination.Pipeline == nil || len(team.Spec.Coordination.Pipeline.Stages) == 0 {
			return "coordination.pipeline.stages is required when pattern is 'pipeline'"
		}
	}
	for _, a := range team.Spec.Agents {
		harness := a.Harness
		if harness == "" {
			harness = team.Spec.Defaults.Harness
		}
		if harness == "" {
			harness = "sdk"
		}
		if !slices.Contains(clusterCapableHarnesses, harness) {
			return fmt.Sprintf("agent %q: harness %q is local-only and cannot run on cluster (CLI §4.7)", a.Name, harness)
		}
		// prompt + autonomous is invalid headless: an unattended agent cannot
		// block on an interactive prompt (CLI §4.2.1).
		pm := a.PermissionMode
		if pm == "" {
			pm = team.Spec.Defaults.PermissionMode
		}
		autonomy := ""
		if a.Interaction != nil {
			autonomy = a.Interaction.Autonomy
		}
		if autonomy == "" {
			autonomy = team.Spec.Interaction.Autonomy
		}
		if pm == "prompt" && autonomy == "autonomous" {
			return fmt.Sprintf("agent %q: permissionMode 'prompt' with autonomy 'autonomous' is invalid (no interactive surface) (CLI §4.2.1)", a.Name)
		}
	}
	return ""
}

// phaseFor maps the number of live agent pods to the team phase. With no
// provisioned agents yet (scale-to-zero, or pre-Dispatcher M0), the team is
// Pending — the M0 acceptance bar (v2 §14: apply sample → Pending).
func phaseFor(activeAgents int) string {
	if activeAgents > 0 {
		return "Running"
	}
	return "Pending"
}

func countActiveAgents(team *karov1.AgentTeam) int {
	if team.Spec.Runtime != nil && team.Spec.Runtime.ScaleToZero != nil && *team.Spec.Runtime.ScaleToZero {
		return 0 // scale-to-zero: no pods until a task arrives (§5.1)
	}
	return len(team.Spec.Agents)
}

func (r *AgentTeamReconciler) setCondition(team *karov1.AgentTeam, condType string, status metav1.ConditionStatus, reason, msg string) {
	cond := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: team.Generation,
	}
	for i, c := range team.Status.Conditions {
		if c.Type == condType {
			team.Status.Conditions[i] = cond
			return
		}
	}
	team.Status.Conditions = append(team.Status.Conditions, cond)
}

// taskToTeam maps an AgentTask event to its team's reconcile request so that a
// task arriving (e.g. via `karo sling --context`) or changing state wakes the
// team reconciler to scale the right agent up or down (v2 §5.1).
func taskToTeam(_ context.Context, obj client.Object) []reconcile.Request {
	task, ok := obj.(*karov1.AgentTask)
	if !ok || task.Spec.Team == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: task.GetNamespace(), Name: task.Spec.Team},
	}}
}

// SetupWithManager wires the controller.
func (r *AgentTeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1.AgentTeam{}).
		Owns(&karov1.AgentTask{}).
		Watches(&karov1.AgentTask{}, handler.EnqueueRequestsFromMapFunc(taskToTeam)).
		Complete(r)
}
