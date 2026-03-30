package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const evalSuiteFinalizer = "karo.dev/evalsuite-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=evalsuites/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !evalSuite.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&evalSuite, evalSuiteFinalizer) {
			controllerutil.RemoveFinalizer(&evalSuite, evalSuiteFinalizer)
			if err := r.Update(ctx, &evalSuite); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&evalSuite, evalSuiteFinalizer) {
		controllerutil.AddFinalizer(&evalSuite, evalSuiteFinalizer)
		if err := r.Update(ctx, &evalSuite); err != nil {
			return ctrl.Result{}, err
		}
	}

	degraded := false
	var degradedReasons []string

	// Validate agentSpecRef exists.
	var agentSpec karov1alpha1.AgentSpec
	agentSpecKey := types.NamespacedName{
		Name:      evalSuite.Spec.AgentSpecRef.Name,
		Namespace: evalSuite.Namespace,
	}
	if err := r.Get(ctx, agentSpecKey, &agentSpec); err != nil {
		degraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("AgentSpec %q not found", evalSuite.Spec.AgentSpecRef.Name))
		r.Recorder.Eventf(&evalSuite, corev1.EventTypeWarning, "AgentSpecNotFound",
			"Referenced AgentSpec %s not found", evalSuite.Spec.AgentSpecRef.Name)
	}

	// Validate eval cases.
	totalCases := int32(len(evalSuite.Spec.EvalCases))
	if totalCases == 0 {
		degraded = true
		degradedReasons = append(degradedReasons, "no eval cases defined")
	}

	for _, ec := range evalSuite.Spec.EvalCases {
		if ec.ID == "" {
			degraded = true
			degradedReasons = append(degradedReasons, "eval case with empty ID found")
		}
		if len(ec.Assertions) == 0 {
			degraded = true
			degradedReasons = append(degradedReasons, fmt.Sprintf("eval case %q has no assertions", ec.ID))
		}
		// Validate LLM judge assertions have a judgeModelConfigRef.
		for _, assertion := range ec.Assertions {
			if assertion.Type == karov1alpha1.AssertionTypeLLMJudge {
				if assertion.JudgeModelConfigRef == nil {
					degraded = true
					degradedReasons = append(degradedReasons, fmt.Sprintf("eval case %q: llm-judge assertion requires judgeModelConfigRef", ec.ID))
				} else {
					// Validate the referenced ModelConfig exists.
					var mc karov1alpha1.ModelConfig
					mcKey := types.NamespacedName{
						Name:      assertion.JudgeModelConfigRef.Name,
						Namespace: evalSuite.Namespace,
					}
					if err := r.Get(ctx, mcKey, &mc); err != nil {
						degraded = true
						degradedReasons = append(degradedReasons, fmt.Sprintf("eval case %q: judgeModelConfigRef %q not found", ec.ID, assertion.JudgeModelConfigRef.Name))
					}
				}
			}
		}
	}

	// Set AgentSpecReady condition.
	var agentSpecFound bool
	for _, reason := range degradedReasons {
		if strings.Contains(reason, "AgentSpec") {
			agentSpecFound = false
			break
		}
	}
	if !degraded {
		agentSpecFound = true
	}
	if agentSpecFound {
		setCondition(&evalSuite.Status.Conditions, metav1.Condition{
			Type:               "AgentSpecReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: evalSuite.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AgentSpecFound",
			Message:            fmt.Sprintf("AgentSpec %s is available", evalSuite.Spec.AgentSpecRef.Name),
		})
	} else {
		setCondition(&evalSuite.Status.Conditions, metav1.Condition{
			Type:               "AgentSpecReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: evalSuite.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AgentSpecNotFound",
			Message:            fmt.Sprintf("AgentSpec %s not found", evalSuite.Spec.AgentSpecRef.Name),
		})
	}

	// Update status.
	evalSuite.Status.TotalCases = totalCases

	if degraded {
		evalSuite.Status.Phase = "Degraded"
		setCondition(&evalSuite.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: evalSuite.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ValidationFailed",
			Message:            fmt.Sprintf("EvalSuite validation failed: %v", degradedReasons),
		})
	} else {
		evalSuite.Status.Phase = "Ready"
		setCondition(&evalSuite.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: evalSuite.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "EvalSuiteReady",
			Message:            fmt.Sprintf("EvalSuite is ready with %d eval cases", totalCases),
		})
	}

	if err := r.Status().Update(ctx, &evalSuite); err != nil {
		logger.Error(err, "unable to update EvalSuite status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&evalSuite, "Normal", "Reconciled", fmt.Sprintf("EvalSuite reconciled with %d cases", totalCases))
	logger.Info("reconciled EvalSuite", "totalCases", evalSuite.Status.TotalCases, "phase", evalSuite.Status.Phase)
	return ctrl.Result{}, nil
}

func (r *EvalSuiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.EvalSuite{}).
		Complete(r)
}
