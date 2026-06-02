package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1 "github.com/joe2far/karo/operator/api/v1"
)

// AgentTaskReconciler keeps the K8s-visible AgentTask projection in sync.
//
// The authoritative task record lives in the tasks backend (Postgres); this
// controller reflects state transitions for kubectl/tooling visibility and
// requests pods for zero-scaled agents when a task targets them (v2 §3.2, §5.1).
type AgentTaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=karo.dev,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agenttasks/status,verbs=get;update;patch

// Reconcile observes an AgentTask projection.
func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var task karov1.AgentTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Default a blank task to pending and stamp the transition. The tasks backend
	// is the source of truth; this projection only reflects state for kubectl/
	// tooling visibility — no orchestration decisions are made here (v2 §3.2, §12).
	if task.Status.State == "" {
		now := metav1.Now()
		task.Status.State = "pending"
		task.Status.LastTransition = &now
		if err := r.Status().Update(ctx, &task); err != nil {
			return ctrl.Result{}, err
		}
	}
	logger.V(1).Info("observed task", "task", task.Name, "state", task.Status.State)
	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller.
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1.AgentTask{}).
		Complete(r)
}
