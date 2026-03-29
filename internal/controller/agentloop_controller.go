package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
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

	// Create/update CronJobs for cron triggers.
	for _, trigger := range loop.Spec.Triggers {
		if trigger.Type != "cron" {
			continue
		}
		if err := r.ensureCronJob(ctx, &loop, trigger.Schedule); err != nil {
			logger.Error(err, "failed to ensure CronJob for cron trigger")
			return ctrl.Result{}, err
		}
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

// ensureCronJob creates or updates a CronJob for a cron trigger.
func (r *AgentLoopReconciler) ensureCronJob(ctx context.Context, loop *karov1alpha1.AgentLoop, schedule string) error {
	cronJobName := fmt.Sprintf("%s-cron", loop.Name)
	var existing batchv1.CronJob
	cronJobKey := types.NamespacedName{Name: cronJobName, Namespace: loop.Namespace}

	desired := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cronJobName,
			Namespace: loop.Namespace,
			Labels: map[string]string{
				"karo.dev/agentloop": loop.Name,
			},
		},
		Spec: batchv1.CronJobSpec{
			Schedule: schedule,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:  "loop-trigger",
									Image: "ghcr.io/karo-dev/loop-trigger:latest",
									Env: []corev1.EnvVar{
										{Name: "KARO_AGENTLOOP", Value: loop.Name},
										{Name: "KARO_NAMESPACE", Value: loop.Namespace},
										{Name: "KARO_DISPATCHER", Value: loop.Spec.DispatcherRef.Name},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference so CronJob is cleaned up with the AgentLoop.
	if err := ctrl.SetControllerReference(loop, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference on CronJob: %w", err)
	}

	if err := r.Get(ctx, cronJobKey, &existing); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		// Create the CronJob.
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create CronJob %s: %w", cronJobName, err)
		}
		r.Recorder.Eventf(loop, "Normal", "CronJobCreated", "Created CronJob %s with schedule %s", cronJobName, schedule)
		return nil
	}

	// Update if schedule changed.
	if existing.Spec.Schedule != schedule {
		existing.Spec.Schedule = schedule
		if err := r.Update(ctx, &existing); err != nil {
			return fmt.Errorf("failed to update CronJob %s: %w", cronJobName, err)
		}
		r.Recorder.Eventf(loop, "Normal", "CronJobUpdated", "Updated CronJob %s schedule to %s", cronJobName, schedule)
	}

	return nil
}

func (r *AgentLoopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentLoop{}).
		Owns(&batchv1.CronJob{}).
		Complete(r)
}
