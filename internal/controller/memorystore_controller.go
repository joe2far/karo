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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const memoryStoreFinalizer = "karo.dev/memorystore-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=memorystores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=memorystores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=memorystores/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
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
		memoryStore.Status.Phase = PhaseError
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

	// Validate backend-specific configuration.
	backendReachable := true
	var backendMsg string

	switch memoryStore.Spec.Backend.Type {
	case "mem0":
		if memoryStore.Spec.Backend.Mem0 == nil {
			backendReachable = false
			backendMsg = "mem0 backend requires mem0 configuration block"
		} else {
			// Validate API key secret exists.
			secretKey := types.NamespacedName{
				Name:      memoryStore.Spec.Backend.Mem0.APIKeySecret.Name,
				Namespace: memoryStore.Namespace,
			}
			var secret corev1.Secret
			if err := r.Get(ctx, secretKey, &secret); err != nil {
				backendReachable = false
				backendMsg = fmt.Sprintf("mem0 apiKeySecret %q not found", memoryStore.Spec.Backend.Mem0.APIKeySecret.Name)
				r.Recorder.Eventf(&memoryStore, corev1.EventTypeWarning, "SecretNotFound",
					"mem0 API key secret %s not found", memoryStore.Spec.Backend.Mem0.APIKeySecret.Name)
			} else if memoryStore.Spec.Backend.Mem0.APIKeySecret.Key != "" {
				if _, exists := secret.Data[memoryStore.Spec.Backend.Mem0.APIKeySecret.Key]; !exists {
					backendReachable = false
					backendMsg = fmt.Sprintf("key %q not found in secret %q", memoryStore.Spec.Backend.Mem0.APIKeySecret.Key, memoryStore.Spec.Backend.Mem0.APIKeySecret.Name)
				}
			}
			if backendReachable {
				if memoryStore.Spec.Backend.Mem0.OrganizationID == "" || memoryStore.Spec.Backend.Mem0.ProjectID == "" {
					backendReachable = false
					backendMsg = "mem0 backend requires organizationId and projectId"
				} else {
					backendMsg = "mem0 backend configured and credentials validated"
				}
			}
		}
	case "redis", "pgvector":
		// These backends don't have dedicated config structs in the types yet.
		// Mark as configured based on the type being set.
		backendMsg = fmt.Sprintf("%s backend type configured", memoryStore.Spec.Backend.Type)
	default:
		backendReachable = false
		backendMsg = fmt.Sprintf("unsupported backend type: %s (supported: mem0, redis, pgvector)", memoryStore.Spec.Backend.Type)
	}

	// Validate scope.
	switch memoryStore.Spec.Scope {
	case karov1alpha1.MemoryScopeAgentLocal, karov1alpha1.MemoryScopeTeam, karov1alpha1.MemoryScopeOrg:
		// Valid scope.
	default:
		if memoryStore.Spec.Scope != "" {
			backendReachable = false
			backendMsg = fmt.Sprintf("unsupported scope: %s (supported: agent-local, team, org)", memoryStore.Spec.Scope)
		}
	}

	// Validate boundAgents references exist.
	var missingAgents []string
	for _, agentRef := range memoryStore.Spec.BoundAgents {
		var agentSpec karov1alpha1.AgentSpec
		key := types.NamespacedName{Name: agentRef.Name, Namespace: memoryStore.Namespace}
		if err := r.Get(ctx, key, &agentSpec); err != nil {
			missingAgents = append(missingAgents, agentRef.Name)
			r.Recorder.Eventf(&memoryStore, corev1.EventTypeWarning, "BoundAgentNotFound",
				"Bound AgentSpec %s not found", agentRef.Name)
		}
	}
	if len(missingAgents) > 0 {
		setCondition(&memoryStore.Status.Conditions, metav1.Condition{
			Type:               "BoundAgentsReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: memoryStore.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AgentsNotFound",
			Message:            fmt.Sprintf("Bound AgentSpecs not found: %v", missingAgents),
		})
	} else if len(memoryStore.Spec.BoundAgents) > 0 {
		setCondition(&memoryStore.Status.Conditions, metav1.Condition{
			Type:               "BoundAgentsReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: memoryStore.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AllAgentsFound",
			Message:            fmt.Sprintf("All %d bound AgentSpecs found", len(memoryStore.Spec.BoundAgents)),
		})
	}

	// Update status.
	if backendReachable {
		memoryStore.Status.Phase = PhaseReady
		setCondition(&memoryStore.Status.Conditions, metav1.Condition{
			Type:               "BackendReachable",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: memoryStore.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "BackendConfigured",
			Message:            backendMsg,
		})
	} else {
		memoryStore.Status.Phase = PhaseError
		setCondition(&memoryStore.Status.Conditions, metav1.Condition{
			Type:               "BackendReachable",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: memoryStore.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "BackendConfigError",
			Message:            backendMsg,
		})
	}

	if err := r.Status().Update(ctx, &memoryStore); err != nil {
		logger.Error(err, "unable to update MemoryStore status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&memoryStore, "Normal", "Reconciled", fmt.Sprintf("MemoryStore reconciled: %s", backendMsg))
	return ctrl.Result{}, nil
}

func (r *MemoryStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.MemoryStore{}).
		Complete(r)
}
