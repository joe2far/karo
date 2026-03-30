package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		Message:            "AgentPolicy is active and enforcing rules",
	})

	if err := r.Status().Update(ctx, &agentPolicy); err != nil {
		logger.Error(err, "unable to update AgentPolicy status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&agentPolicy, "Normal", "Reconciled", "AgentPolicy reconciled and active")
	return ctrl.Result{}, nil
}

func (r *AgentPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentPolicy{}).
		Complete(r)
}
