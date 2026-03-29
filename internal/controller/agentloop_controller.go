package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/karo-dev/karo/api/v1alpha1"
)

// +kubebuilder:rbac:groups=karo.dev,resources=agentloops,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentloops/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentloops/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// AgentLoopReconciler reconciles an AgentLoop object.
type AgentLoopReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentLoopReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var loop karov1alpha1.AgentLoop
	if err := r.Get(ctx, req.NamespacedName, &loop); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate triggers.
	scheduleValid := true
	var validationMsg string
	for i, trigger := range loop.Spec.Triggers {
		switch trigger.Type {
		case "cron":
			if trigger.Schedule == "" {
				scheduleValid = false
				validationMsg = fmt.Sprintf("trigger[%d]: cron trigger requires a non-empty schedule", i)
			}
		case "event":
			if trigger.Source == nil {
				scheduleValid = false
				validationMsg = fmt.Sprintf("trigger[%d]: event trigger requires a source", i)
			}
		case "webhook":
			// Webhook triggers are valid without additional config.
		default:
			scheduleValid = false
			validationMsg = fmt.Sprintf("trigger[%d]: unknown trigger type %q", i, trigger.Type)
		}
	}

	if len(loop.Spec.Triggers) == 0 {
		scheduleValid = false
		validationMsg = "at least one trigger is required"
	}

	// Set ScheduleValid condition.
	if scheduleValid {
		setCondition(&loop.Status.Conditions, metav1.Condition{
			Type:               "ScheduleValid",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: loop.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "Valid",
			Message:            "All triggers are valid",
		})
	} else {
		setCondition(&loop.Status.Conditions, metav1.Condition{
			Type:               "ScheduleValid",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: loop.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "Invalid",
			Message:            validationMsg,
		})
		loop.Status.Phase = "Suspended"
		if err := r.Status().Update(ctx, &loop); err != nil {
			logger.Error(err, "unable to update AgentLoop status")
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&loop, "Warning", "InvalidTrigger", validationMsg)
		return ctrl.Result{}, nil
	}

	// Check eval gate if configured.
	if loop.Spec.EvalGate != nil {
		var evalSuite karov1alpha1.EvalSuite
		evalSuiteKey := client.ObjectKey{
			Name:      loop.Spec.EvalGate.EvalSuiteRef.Name,
			Namespace: loop.Namespace,
		}
		if err := r.Get(ctx, evalSuiteKey, &evalSuite); err != nil {
			logger.Info("eval gate EvalSuite not found, blocking loop", "evalSuite", evalSuiteKey.Name)
			loop.Status.Phase = "GateBlocked"
			setCondition(&loop.Status.Conditions, metav1.Condition{
				Type:               "EvalGateReady",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: loop.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "EvalSuiteNotFound",
				Message:            fmt.Sprintf("EvalSuite %s not found", evalSuiteKey.Name),
			})
			if statusErr := r.Status().Update(ctx, &loop); statusErr != nil {
				logger.Error(statusErr, "unable to update AgentLoop status")
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		setCondition(&loop.Status.Conditions, metav1.Condition{
			Type:               "EvalGateReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: loop.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "EvalSuiteFound",
			Message:            fmt.Sprintf("EvalSuite %s is available", evalSuiteKey.Name),
		})
	}

	// All checks passed -- set phase to Active.
	loop.Status.Phase = "Active"

	setCondition(&loop.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: loop.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Reconciled",
		Message:            "AgentLoop is active",
	})

	if err := r.Status().Update(ctx, &loop); err != nil {
		logger.Error(err, "unable to update AgentLoop status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&loop, "Normal", "Reconciled", "AgentLoop reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *AgentLoopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentLoop{}).
		Complete(r)
}
