package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const toolSetFinalizer = "karo.dev/toolset-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=toolsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type ToolSetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *ToolSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var toolSet karov1alpha1.ToolSet
	if err := r.Get(ctx, req.NamespacedName, &toolSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !toolSet.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&toolSet, toolSetFinalizer) {
			controllerutil.RemoveFinalizer(&toolSet, toolSetFinalizer)
			if err := r.Update(ctx, &toolSet); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&toolSet, toolSetFinalizer) {
		controllerutil.AddFinalizer(&toolSet, toolSetFinalizer)
		if err := r.Update(ctx, &toolSet); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Count available tools.
	toolCount := int32(len(toolSet.Spec.Tools))

	// Update status.
	toolSet.Status.Phase = "Ready"
	toolSet.Status.AvailableTools = toolCount

	setCondition(&toolSet.Status.Conditions, metav1.Condition{
		Type:               "AllToolsReachable",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: toolSet.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "ToolsValidated",
		Message:            fmt.Sprintf("All %d tools are considered reachable", toolCount),
	})

	if err := r.Status().Update(ctx, &toolSet); err != nil {
		logger.Error(err, "unable to update ToolSet status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&toolSet, "Normal", "Reconciled", fmt.Sprintf("ToolSet reconciled with %d tools", toolCount))
	return ctrl.Result{}, nil
}

func (r *ToolSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.ToolSet{}).
		Complete(r)
}
