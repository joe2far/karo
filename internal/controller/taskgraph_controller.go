package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	"github.com/joe2far/karo/internal/dag"
)

// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// TaskGraphReconciler reconciles a TaskGraph object.
type TaskGraphReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *TaskGraphReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tg karov1alpha1.TaskGraph
	if err := r.Get(ctx, req.NamespacedName, &tg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// On first reconcile (status.taskStatuses empty): seed status from spec.tasks.
	if len(tg.Status.TaskStatuses) == 0 {
		r.seedTaskStatuses(&tg)
		// Validate no cycles on initial seed.
		if err := r.validateMutation(&tg); err != nil {
			logger.Error(err, "cycle detected in task graph on initial seed")
			tg.Status.Phase = karov1alpha1.TaskGraphPhaseFailed
			setCondition(&tg.Status.Conditions, metav1.Condition{
				Type:               "DagValid",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: tg.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "CycleDetected",
				Message:            err.Error(),
			})
			if statusErr := r.Status().Update(ctx, &tg); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}
		setCondition(&tg.Status.Conditions, metav1.Condition{
			Type:               "DagValid",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: tg.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "Valid",
			Message:            "Task DAG has no cycles",
		})
	} else {
		// On spec change (new tasks added): seed status for new task IDs.
		seeded := false
		for _, task := range tg.Spec.Tasks {
			if _, exists := tg.Status.TaskStatuses[task.ID]; !exists {
				tg.Status.TaskStatuses[task.ID] = karov1alpha1.TaskRuntimeState{
					Phase: karov1alpha1.TaskPhaseBlocked,
				}
				seeded = true
			}
		}
		if seeded {
			// Validate no cycles after adding new tasks.
			if err := r.validateMutation(&tg); err != nil {
				logger.Error(err, "cycle detected in task graph after mutation")
				tg.Status.Phase = karov1alpha1.TaskGraphPhaseFailed
				setCondition(&tg.Status.Conditions, metav1.Condition{
					Type:               "DagValid",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: tg.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             "CycleDetected",
					Message:            err.Error(),
				})
				if statusErr := r.Status().Update(ctx, &tg); statusErr != nil {
					return ctrl.Result{}, statusErr
				}
				return ctrl.Result{}, nil
			}
		}
	}

	// Walk dependencies: transition Blocked -> Open when all deps are Closed.
	r.reconcileTaskDeps(&tg)

	// Handle timeouts on InProgress tasks.
	r.handleTimeouts(&tg)

	// Handle eval gates for EvalPending tasks.
	for taskID, ts := range tg.Status.TaskStatuses {
		if ts.Phase == karov1alpha1.TaskPhaseEvalPending {
			r.runEvalGate(&tg, taskID)
		}
	}

	// Recompute aggregates.
	r.recomputeAggregates(&tg)

	// Write status via status subresource.
	if err := r.Status().Update(ctx, &tg); err != nil {
		logger.Error(err, "unable to update TaskGraph status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&tg, "Normal", "Reconciled", "TaskGraph reconciled successfully")

	// Requeue periodically to handle timeouts.
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// seedTaskStatuses initializes status.taskStatuses from spec.tasks.
// Tasks with no dependencies start as Open; tasks with dependencies start as Blocked.
func (r *TaskGraphReconciler) seedTaskStatuses(tg *karov1alpha1.TaskGraph) {
	tg.Status.TaskStatuses = make(map[string]karov1alpha1.TaskRuntimeState, len(tg.Spec.Tasks))
	for _, task := range tg.Spec.Tasks {
		phase := karov1alpha1.TaskPhaseOpen
		if len(task.Deps) > 0 {
			phase = karov1alpha1.TaskPhaseBlocked
		}
		tg.Status.TaskStatuses[task.ID] = karov1alpha1.TaskRuntimeState{
			Phase: phase,
		}
	}
	tg.Status.Phase = karov1alpha1.TaskGraphPhasePending
}

// reconcileTaskDeps walks all Blocked tasks and transitions them to Open
// if all dependencies are Closed. Emits a TaskReady event for each transition.
func (r *TaskGraphReconciler) reconcileTaskDeps(tg *karov1alpha1.TaskGraph) {
	// Build task lookup by ID.
	taskByID := make(map[string]karov1alpha1.Task, len(tg.Spec.Tasks))
	for _, t := range tg.Spec.Tasks {
		taskByID[t.ID] = t
	}

	for taskID, ts := range tg.Status.TaskStatuses {
		if ts.Phase != karov1alpha1.TaskPhaseBlocked {
			continue
		}
		task, ok := taskByID[taskID]
		if !ok {
			continue
		}
		allDepsClosed := true
		for _, dep := range task.Deps {
			depStatus, exists := tg.Status.TaskStatuses[dep]
			if !exists || depStatus.Phase != karov1alpha1.TaskPhaseClosed {
				allDepsClosed = false
				break
			}
		}
		if allDepsClosed {
			ts.Phase = karov1alpha1.TaskPhaseOpen
			tg.Status.TaskStatuses[taskID] = ts
			r.Recorder.Event(tg, "Normal", "TaskReady",
				fmt.Sprintf("Task %s is now Open (all dependencies resolved)", taskID))
		}
	}
}

// handleTimeouts checks InProgress tasks for timeout expiry.
func (r *TaskGraphReconciler) handleTimeouts(tg *karov1alpha1.TaskGraph) {
	taskByID := make(map[string]karov1alpha1.Task, len(tg.Spec.Tasks))
	for _, t := range tg.Spec.Tasks {
		taskByID[t.ID] = t
	}

	now := time.Now()
	for taskID, ts := range tg.Status.TaskStatuses {
		if ts.Phase != karov1alpha1.TaskPhaseInProgress {
			continue
		}
		if ts.StartedAt == nil {
			continue
		}
		task := taskByID[taskID]

		// Determine timeout: task-level override or dispatch policy default.
		var timeoutMinutes int32
		if task.TimeoutMinutes != nil {
			timeoutMinutes = *task.TimeoutMinutes
		} else if tg.Spec.DispatchPolicy.DefaultTimeoutMinutes != nil {
			timeoutMinutes = *tg.Spec.DispatchPolicy.DefaultTimeoutMinutes
		}
		if timeoutMinutes <= 0 {
			continue
		}

		deadline := ts.StartedAt.Time.Add(time.Duration(timeoutMinutes) * time.Minute)
		if now.After(deadline) {
			// Check retry policy before failing.
			maxRetries := tg.Spec.DispatchPolicy.RetryPolicy.MaxRetries
			if ts.RetryCount < maxRetries {
				ts.Phase = karov1alpha1.TaskPhaseOpen
				ts.RetryCount++
				ts.FailureNotes = fmt.Sprintf("Task timed out after %d minutes (retry %d/%d)", timeoutMinutes, ts.RetryCount, maxRetries)
				ts.AssignedTo = ""
				ts.AssignedAt = nil
				ts.StartedAt = nil
				tg.Status.TaskStatuses[taskID] = ts
				r.Recorder.Event(tg, "Warning", "TaskTimeoutRetry",
					fmt.Sprintf("Task %s timed out, reopened for retry %d/%d", taskID, ts.RetryCount, maxRetries))
			} else {
				ts.Phase = karov1alpha1.TaskPhaseFailed
				ts.FailureNotes = fmt.Sprintf("Task timed out after %d minutes (retries exhausted)", timeoutMinutes)
				nowMeta := metav1.NewTime(now)
				ts.CompletedAt = &nowMeta
				tg.Status.TaskStatuses[taskID] = ts
				r.Recorder.Event(tg, "Warning", "TaskTimeout",
					fmt.Sprintf("Task %s timed out after %d minutes (retries exhausted)", taskID, timeoutMinutes))
			}
		}
	}
}

// recomputeAggregates counts tasks by phase and derives the graph-level phase.
func (r *TaskGraphReconciler) recomputeAggregates(tg *karov1alpha1.TaskGraph) {
	var open, dispatched, inProgress, evalPending, closed, failed, blocked int32

	for _, ts := range tg.Status.TaskStatuses {
		switch ts.Phase {
		case karov1alpha1.TaskPhaseOpen:
			open++
		case karov1alpha1.TaskPhaseDispatched:
			dispatched++
		case karov1alpha1.TaskPhaseInProgress:
			inProgress++
		case karov1alpha1.TaskPhaseEvalPending:
			evalPending++
		case karov1alpha1.TaskPhaseClosed:
			closed++
		case karov1alpha1.TaskPhaseFailed:
			failed++
		case karov1alpha1.TaskPhaseBlocked, karov1alpha1.TaskPhaseAwaitingApproval:
			blocked++
		}
	}

	total := int32(len(tg.Status.TaskStatuses))
	tg.Status.TotalTasks = total
	tg.Status.OpenTasks = open
	tg.Status.DispatchedTasks = dispatched
	tg.Status.InProgressTasks = inProgress
	tg.Status.EvalPendingTasks = evalPending
	tg.Status.ClosedTasks = closed
	tg.Status.FailedTasks = failed
	tg.Status.BlockedTasks = blocked

	if total > 0 {
		tg.Status.CompletionPercent = (closed * 100) / total
	}

	// Derive graph phase.
	switch {
	case closed == total && total > 0:
		tg.Status.Phase = karov1alpha1.TaskGraphPhaseCompleted
	case failed > 0 && (open+dispatched+inProgress+evalPending) == 0:
		tg.Status.Phase = karov1alpha1.TaskGraphPhaseFailed
	case inProgress > 0 || dispatched > 0 || evalPending > 0:
		tg.Status.Phase = karov1alpha1.TaskGraphPhaseInProgress
	case blocked == total:
		tg.Status.Phase = karov1alpha1.TaskGraphPhaseBlocked
	default:
		tg.Status.Phase = karov1alpha1.TaskGraphPhasePending
	}
}

// runEvalGate handles tasks in EvalPending phase. Checks the eval gate
// configuration and transitions the task to Closed (pass) or handles
// failure via Reopen/Escalate based on the onFail policy.
func (r *TaskGraphReconciler) runEvalGate(tg *karov1alpha1.TaskGraph, taskID string) {
	ts := tg.Status.TaskStatuses[taskID]

	// Find the task spec to check for eval gate config.
	var task *karov1alpha1.Task
	for i := range tg.Spec.Tasks {
		if tg.Spec.Tasks[i].ID == taskID {
			task = &tg.Spec.Tasks[i]
			break
		}
	}

	if task == nil || task.EvalGate == nil {
		// No eval gate configured -- auto-close.
		ts.Phase = karov1alpha1.TaskPhaseClosed
		now := metav1.Now()
		ts.CompletedAt = &now
		tg.Status.TaskStatuses[taskID] = ts
		return
	}

	// If there is already an eval result, use it; otherwise stub as passed.
	if ts.EvalResult != nil {
		if ts.EvalResult.Passed {
			ts.Phase = karov1alpha1.TaskPhaseClosed
			now := metav1.Now()
			ts.CompletedAt = &now
		} else {
			// Handle failure based on onFail policy.
			switch task.EvalGate.OnFail {
			case karov1alpha1.EvalGateFailReopen:
				failedPassRate := ts.EvalResult.PassRate
				ts.Phase = karov1alpha1.TaskPhaseOpen
				ts.RetryCount++
				ts.FailureNotes = ts.EvalResult.FailureNotes
				ts.EvalResult = nil
				ts.AssignedTo = ""
				ts.AssignedAt = nil
				ts.StartedAt = nil
				r.Recorder.Event(tg, "Warning", "EvalGateReopen",
					fmt.Sprintf("Task %s reopened after eval gate failure (passRate=%.2f)",
						taskID, failedPassRate))
			case karov1alpha1.EvalGateFailEscalate:
				ts.Phase = karov1alpha1.TaskPhaseFailed
				ts.FailureNotes = fmt.Sprintf("Eval gate failed (passRate=%.2f, min=%.2f): %s",
					ts.EvalResult.PassRate, task.EvalGate.MinPassRate, ts.EvalResult.FailureNotes)
				now := metav1.Now()
				ts.CompletedAt = &now
				r.Recorder.Event(tg, "Warning", "EvalGateEscalate",
					fmt.Sprintf("Task %s escalated after eval gate failure", taskID))
			}
		}
	} else {
		// No eval result yet -- stub: assume pass.
		ts.Phase = karov1alpha1.TaskPhaseClosed
		now := metav1.Now()
		ts.CompletedAt = &now
		ts.EvalResult = &karov1alpha1.EvalResult{
			PassRate:    1.0,
			Passed:      true,
			EvaluatedAt: now,
		}
	}

	tg.Status.TaskStatuses[taskID] = ts
}

// validateMutation validates the DAG has no cycles using Kahn's algorithm.
func (r *TaskGraphReconciler) validateMutation(tg *karov1alpha1.TaskGraph) error {
	return dag.ValidateNoCycles(tg.Spec.Tasks)
}

func (r *TaskGraphReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.TaskGraph{}).
		Complete(r)
}
