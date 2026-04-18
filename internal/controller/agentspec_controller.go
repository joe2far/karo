package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=memorystores,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=sandboxclasses,verbs=get;list;watch

// AgentSpecReconciler reconciles a AgentSpec object
type AgentSpecReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentSpecReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agentSpec karov1alpha1.AgentSpec
	if err := r.Get(ctx, req.NamespacedName, &agentSpec); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch AgentSpec")
		return ctrl.Result{}, err
	}

	degraded := false
	var degradedReasons []string

	// Resolve modelConfigRef — required
	var modelConfig karov1alpha1.ModelConfig
	modelConfigKey := types.NamespacedName{
		Name:      agentSpec.Spec.ModelConfigRef.Name,
		Namespace: agentSpec.Namespace,
	}
	modelConfigReady := false
	if err := r.Get(ctx, modelConfigKey, &modelConfig); err != nil {
		degraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("ModelConfig %q not found", agentSpec.Spec.ModelConfigRef.Name))
		r.Recorder.Eventf(&agentSpec, corev1.EventTypeWarning, "ModelConfigNotFound",
			"Referenced ModelConfig %s not found", agentSpec.Spec.ModelConfigRef.Name)
	} else if modelConfig.Status.Phase != PhaseReady {
		degraded = true
		degradedReasons = append(degradedReasons, fmt.Sprintf("ModelConfig %q is not Ready (phase: %s)", modelConfig.Name, modelConfig.Status.Phase))
	} else {
		modelConfigReady = true
	}

	// Check memoryRef if set
	if agentSpec.Spec.MemoryRef != nil {
		var memoryStore karov1alpha1.MemoryStore
		key := types.NamespacedName{
			Name:      agentSpec.Spec.MemoryRef.Name,
			Namespace: agentSpec.Namespace,
		}
		if err := r.Get(ctx, key, &memoryStore); err != nil {
			degraded = true
			degradedReasons = append(degradedReasons, fmt.Sprintf("MemoryStore %q not found", agentSpec.Spec.MemoryRef.Name))
			r.Recorder.Eventf(&agentSpec, corev1.EventTypeWarning, "MemoryStoreNotFound",
				"Referenced MemoryStore %s not found", agentSpec.Spec.MemoryRef.Name)
		}
	}

	// Check toolSetRef if set
	if agentSpec.Spec.ToolSetRef != nil {
		var toolSet karov1alpha1.ToolSet
		key := types.NamespacedName{
			Name:      agentSpec.Spec.ToolSetRef.Name,
			Namespace: agentSpec.Namespace,
		}
		if err := r.Get(ctx, key, &toolSet); err != nil {
			degraded = true
			degradedReasons = append(degradedReasons, fmt.Sprintf("ToolSet %q not found", agentSpec.Spec.ToolSetRef.Name))
			r.Recorder.Eventf(&agentSpec, corev1.EventTypeWarning, "ToolSetNotFound",
				"Referenced ToolSet %s not found", agentSpec.Spec.ToolSetRef.Name)
		}
	}

	// Check sandboxClassRef if set
	if agentSpec.Spec.SandboxClassRef != nil {
		var sandboxClass karov1alpha1.SandboxClass
		key := types.NamespacedName{
			Name:      agentSpec.Spec.SandboxClassRef.Name,
			Namespace: agentSpec.Namespace,
		}
		if err := r.Get(ctx, key, &sandboxClass); err != nil {
			degraded = true
			degradedReasons = append(degradedReasons, fmt.Sprintf("SandboxClass %q not found", agentSpec.Spec.SandboxClassRef.Name))
			r.Recorder.Eventf(&agentSpec, corev1.EventTypeWarning, "SandboxClassNotFound",
				"Referenced SandboxClass %s not found", agentSpec.Spec.SandboxClassRef.Name)
		}
	}

	// Set phase
	if degraded {
		agentSpec.Status.Phase = PhaseDegraded
	} else {
		agentSpec.Status.Phase = PhaseReady
	}

	now := metav1.Now()
	agentSpec.Status.LastUpdated = &now

	// Set Ready condition
	if degraded {
		readyCondition := metav1.Condition{
			Type:               PhaseReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: agentSpec.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "DependenciesDegraded",
			Message:            fmt.Sprintf("One or more dependencies are missing or not ready: %v", degradedReasons),
		}
		setCondition(&agentSpec.Status.Conditions, readyCondition)
	} else {
		readyCondition := metav1.Condition{
			Type:               PhaseReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: agentSpec.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AllDependenciesReady",
			Message:            "All referenced resources are available and ready",
		}
		setCondition(&agentSpec.Status.Conditions, readyCondition)
	}

	// Set ModelConfigReady condition
	if modelConfigReady {
		mcCondition := metav1.Condition{
			Type:               "ModelConfigReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: agentSpec.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ModelConfigAvailable",
			Message:            fmt.Sprintf("ModelConfig %s is ready", agentSpec.Spec.ModelConfigRef.Name),
		}
		setCondition(&agentSpec.Status.Conditions, mcCondition)
	} else {
		mcCondition := metav1.Condition{
			Type:               "ModelConfigReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: agentSpec.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ModelConfigNotReady",
			Message:            fmt.Sprintf("ModelConfig %s is not available or not ready", agentSpec.Spec.ModelConfigRef.Name),
		}
		setCondition(&agentSpec.Status.Conditions, mcCondition)
	}

	if err := r.Status().Update(ctx, &agentSpec); err != nil {
		logger.Error(err, "unable to update AgentSpec status")
		return ctrl.Result{}, err
	}

	logger.Info("reconciled AgentSpec", "phase", agentSpec.Status.Phase)
	return ctrl.Result{}, nil
}

func (r *AgentSpecReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentSpec{}).
		Complete(r)
}
