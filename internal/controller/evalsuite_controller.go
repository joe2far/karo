package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/karo-dev/karo/api/v1alpha1"
)

// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites/finalizers,verbs=update

// EvalSuiteReconciler reconciles a EvalSuite object
type EvalSuiteReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *EvalSuiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var evalSuite karov1alpha1.EvalSuite
	if err := r.Get(ctx, req.NamespacedName, &evalSuite); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch EvalSuite")
		return ctrl.Result{}, err
	}

	// Update status
	evalSuite.Status.Phase = "Ready"
	evalSuite.Status.TotalCases = int32(len(evalSuite.Spec.EvalCases))

	// Set Ready condition
	readyCondition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: evalSuite.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "EvalSuiteReady",
		Message:            "EvalSuite is ready with all eval cases loaded",
	}
	setCondition(&evalSuite.Status.Conditions, readyCondition)

	if err := r.Status().Update(ctx, &evalSuite); err != nil {
		logger.Error(err, "unable to update EvalSuite status")
		return ctrl.Result{}, err
	}

	logger.Info("reconciled EvalSuite", "totalCases", evalSuite.Status.TotalCases)
	return ctrl.Result{}, nil
}

func (r *EvalSuiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.EvalSuite{}).
		Complete(r)
}
