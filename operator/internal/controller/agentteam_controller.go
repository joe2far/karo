package controller

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

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

	// 3-4. Provision agents (scale-to-zero default registers with Dispatcher;
	//      eager provisioning would create one pod per agent §5.4).
	team.Status.ActiveAgents = countActiveAgents(&team)

	// 6. Budget status reflects the authoritative counter (status only; §5.6).
	if team.Spec.Budgets != nil && team.Spec.Budgets.Team != nil {
		tb := team.Spec.Budgets.Team
		team.Status.Budget = &karov1.BudgetStatus{
			Provider: tb.Provider, Limit: tb.Limit, Window: tb.Window,
		}
	}

	// 7. Update status.
	if team.Status.Phase == "" {
		team.Status.Phase = "Running"
	}
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
	}
	return ""
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

// SetupWithManager wires the controller.
func (r *AgentTeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1.AgentTeam{}).
		Owns(&karov1.AgentTask{}).
		Complete(r)
}
