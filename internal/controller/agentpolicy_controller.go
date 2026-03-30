package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const agentPolicyFinalizer = "karo.dev/agentpolicy-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=agentpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type AgentPolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agentPolicy karov1alpha1.AgentPolicy
	if err := r.Get(ctx, req.NamespacedName, &agentPolicy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !agentPolicy.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&agentPolicy, agentPolicyFinalizer) {
			controllerutil.RemoveFinalizer(&agentPolicy, agentPolicyFinalizer)
			if err := r.Update(ctx, &agentPolicy); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&agentPolicy, agentPolicyFinalizer) {
		controllerutil.AddFinalizer(&agentPolicy, agentPolicyFinalizer)
		if err := r.Update(ctx, &agentPolicy); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate targetSelector by converting it.
	selector, err := metav1.LabelSelectorAsSelector(&agentPolicy.Spec.TargetSelector)
	if err != nil {
		agentPolicy.Status.Phase = "Error"
		setCondition(&agentPolicy.Status.Conditions, metav1.Condition{
			Type:               "Active",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: agentPolicy.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "InvalidSelector",
			Message:            fmt.Sprintf("Invalid targetSelector: %v", err),
		})
		if statusErr := r.Status().Update(ctx, &agentPolicy); statusErr != nil {
			logger.Error(statusErr, "unable to update AgentPolicy status")
			return ctrl.Result{}, statusErr
		}
		r.Recorder.Event(&agentPolicy, "Warning", "InvalidSelector", fmt.Sprintf("Invalid targetSelector: %v", err))
		return ctrl.Result{}, nil
	}

	// Count matching AgentSpecs to report scope.
	var agentSpecs karov1alpha1.AgentSpecList
	if err := r.List(ctx, &agentSpecs, &client.ListOptions{
		Namespace: agentPolicy.Namespace,
	}); err != nil {
		logger.Error(err, "failed to list AgentSpecs for policy scope")
	}

	var matchingAgents int32
	for _, spec := range agentSpecs.Items {
		if selector.Matches(labels.Set(spec.Labels)) {
			matchingAgents++
		}
	}

	// Validate policy rules.
	var warnings []string

	// Validate model constraints.
	if len(agentPolicy.Spec.Models.AllowedProviders) == 0 && len(agentPolicy.Spec.Models.DeniedModels) == 0 {
		// No model constraints — that's fine, just informational.
	}

	// Validate loop governance bounds.
	if agentPolicy.Spec.Loop.MaxIterationsPerRun > 0 &&
		agentPolicy.Spec.Loop.RequireHumanApprovalAfterIterations > agentPolicy.Spec.Loop.MaxIterationsPerRun {
		warnings = append(warnings, "requireHumanApprovalAfterIterations exceeds maxIterationsPerRun")
	}

	// Validate escalation policy.
	switch agentPolicy.Spec.Escalation.OnPolicyViolation {
	case "Block", "Warn", "Audit", "":
		// Valid values.
	default:
		warnings = append(warnings, fmt.Sprintf("unknown escalation policy: %s", agentPolicy.Spec.Escalation.OnPolicyViolation))
	}

	// Update status.
	agentPolicy.Status.Phase = "Active"
	now := metav1.Now()
	agentPolicy.Status.LastEvaluatedAt = &now

	setCondition(&agentPolicy.Status.Conditions, metav1.Condition{
		Type:               "Active",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: agentPolicy.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "PolicyActive",
		Message:            fmt.Sprintf("AgentPolicy is active, targeting %d agents", matchingAgents),
	})

	if len(warnings) > 0 {
		setCondition(&agentPolicy.Status.Conditions, metav1.Condition{
			Type:               "ConfigValid",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: agentPolicy.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ConfigWarnings",
			Message:            fmt.Sprintf("Policy configuration warnings: %v", warnings),
		})
	} else {
		setCondition(&agentPolicy.Status.Conditions, metav1.Condition{
			Type:               "ConfigValid",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: agentPolicy.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ConfigValid",
			Message:            "Policy configuration is valid",
		})
	}

	if err := r.Status().Update(ctx, &agentPolicy); err != nil {
		logger.Error(err, "unable to update AgentPolicy status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&agentPolicy, "Normal", "Reconciled",
		fmt.Sprintf("AgentPolicy reconciled, targeting %d agents", matchingAgents))
	return ctrl.Result{}, nil
}

func (r *AgentPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentPolicy{}).
		Complete(r)
}
