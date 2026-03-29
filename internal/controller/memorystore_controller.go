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

const memoryStoreFinalizer = "karo.dev/memorystore-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=memorystores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=memorystores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=memorystores/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type MemoryStoreReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *MemoryStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var memoryStore karov1alpha1.MemoryStore
	if err := r.Get(ctx, req.NamespacedName, &memoryStore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !memoryStore.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&memoryStore, memoryStoreFinalizer) {
			controllerutil.RemoveFinalizer(&memoryStore, memoryStoreFinalizer)
			if err := r.Update(ctx, &memoryStore); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&memoryStore, memoryStoreFinalizer) {
		controllerutil.AddFinalizer(&memoryStore, memoryStoreFinalizer)
		if err := r.Update(ctx, &memoryStore); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate backend type.
	if memoryStore.Spec.Backend.Type == "" {
		logger.Info("MemoryStore has no backend type set")
		memoryStore.Status.Phase = "Error"
		setCondition(&memoryStore.Status.Conditions, metav1.Condition{
			Type:               "BackendReachable",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: memoryStore.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "MissingBackendType",
			Message:            "Backend type must not be empty",
		})
		if err := r.Status().Update(ctx, &memoryStore); err != nil {
			logger.Error(err, "unable to update MemoryStore status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update status.
	memoryStore.Status.Phase = "Ready"

	setCondition(&memoryStore.Status.Conditions, metav1.Condition{
		Type:               "BackendReachable",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: memoryStore.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "BackendConfigured",
		Message:            "Backend type is set and considered reachable",
	})

	if err := r.Status().Update(ctx, &memoryStore); err != nil {
		logger.Error(err, "unable to update MemoryStore status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&memoryStore, "Normal", "Reconciled", "MemoryStore reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *MemoryStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.MemoryStore{}).
		Complete(r)
}
