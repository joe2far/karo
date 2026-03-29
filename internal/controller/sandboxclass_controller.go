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

	karov1alpha1 "github.com/karo-dev/karo/api/v1alpha1"
)

const sandboxClassFinalizer = "karo.dev/sandboxclass-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=sandboxclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=sandboxclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=sandboxclasses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type SandboxClassReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *SandboxClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var sandboxClass karov1alpha1.SandboxClass
	if err := r.Get(ctx, req.NamespacedName, &sandboxClass); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !sandboxClass.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&sandboxClass, sandboxClassFinalizer) {
			controllerutil.RemoveFinalizer(&sandboxClass, sandboxClassFinalizer)
			if err := r.Update(ctx, &sandboxClass); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&sandboxClass, sandboxClassFinalizer) {
		controllerutil.AddFinalizer(&sandboxClass, sandboxClassFinalizer)
		if err := r.Update(ctx, &sandboxClass); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate runtimeClassName.
	runtimeClassAvailable := sandboxClass.Spec.RuntimeClassName != ""
	if !runtimeClassAvailable {
		logger.Info("SandboxClass has no runtimeClassName set")
	}

	// Update status.
	sandboxClass.Status.Phase = "Ready"
	sandboxClass.Status.RuntimeClassAvailable = runtimeClassAvailable

	conditionStatus := metav1.ConditionTrue
	conditionMessage := "RuntimeClass is available"
	if !runtimeClassAvailable {
		conditionStatus = metav1.ConditionFalse
		conditionMessage = "RuntimeClassName is not set"
	}

	setCondition(&sandboxClass.Status.Conditions, metav1.Condition{
		Type:               "RuntimeClassAvailable",
		Status:             conditionStatus,
		ObservedGeneration: sandboxClass.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Validated",
		Message:            conditionMessage,
	})

	if err := r.Status().Update(ctx, &sandboxClass); err != nil {
		logger.Error(err, "unable to update SandboxClass status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&sandboxClass, "Normal", "Reconciled", "SandboxClass reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *SandboxClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.SandboxClass{}).
		Complete(r)
}

// setCondition sets or updates a condition in the conditions slice.
func setCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	if conditions == nil {
		return
	}
	for i, c := range *conditions {
		if c.Type == condition.Type {
			if c.Status != condition.Status {
				(*conditions)[i] = condition
			} else {
				// Update fields but keep the original transition time.
				(*conditions)[i].Reason = condition.Reason
				(*conditions)[i].Message = condition.Message
				(*conditions)[i].ObservedGeneration = condition.ObservedGeneration
			}
			return
		}
	}
	*conditions = append(*conditions, condition)
}
