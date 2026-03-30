package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const modelConfigFinalizer = "karo.dev/modelconfig-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type ModelConfigReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *ModelConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var modelConfig karov1alpha1.ModelConfig
	if err := r.Get(ctx, req.NamespacedName, &modelConfig); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !modelConfig.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&modelConfig, modelConfigFinalizer) {
			controllerutil.RemoveFinalizer(&modelConfig, modelConfigFinalizer)
			if err := r.Update(ctx, &modelConfig); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&modelConfig, modelConfigFinalizer) {
		controllerutil.AddFinalizer(&modelConfig, modelConfigFinalizer)
		if err := r.Update(ctx, &modelConfig); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate provider.
	if modelConfig.Spec.Provider == "" {
		modelConfig.Status.Phase = "Error"
		setCondition(&modelConfig.Status.Conditions, metav1.Condition{
			Type:               "CredentialsValid",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: modelConfig.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "MissingProvider",
			Message:            "Provider field must not be empty",
		})
		if err := r.Status().Update(ctx, &modelConfig); err != nil {
			logger.Error(err, "unable to update ModelConfig status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Check credentials based on provider type.
	credentialsValid := false
	credentialMessage := ""

	switch modelConfig.Spec.Provider {
	case "anthropic", "openai":
		if modelConfig.Spec.APIKeySecret != nil {
			credentialsValid = true
			credentialMessage = fmt.Sprintf("API key secret reference is set for provider %s", modelConfig.Spec.Provider)
		} else {
			credentialMessage = fmt.Sprintf("API key secret is required for provider %s", modelConfig.Spec.Provider)
		}
	case "bedrock":
		if modelConfig.Spec.Bedrock != nil {
			credentialsValid = true
			credentialMessage = "Bedrock configuration is present"
		} else {
			credentialMessage = "Bedrock configuration is required for provider bedrock"
		}
	case "vertex":
		if modelConfig.Spec.Vertex != nil {
			credentialsValid = true
			credentialMessage = "Vertex configuration is present"
		} else {
			credentialMessage = "Vertex configuration is required for provider vertex"
		}
	default:
		// For other providers, consider credentials valid if any credential config is present.
		credentialsValid = modelConfig.Spec.APIKeySecret != nil || modelConfig.Spec.Bedrock != nil || modelConfig.Spec.Vertex != nil
		credentialMessage = fmt.Sprintf("Provider %s credential check completed", modelConfig.Spec.Provider)
	}

	// Update status.
	modelConfig.Status.Phase = "Ready"
	modelConfig.Status.Provider = modelConfig.Spec.Provider
	now := metav1.Now()
	modelConfig.Status.LastValidatedAt = &now

	conditionStatus := metav1.ConditionTrue
	reason := "CredentialsFound"
	if !credentialsValid {
		conditionStatus = metav1.ConditionFalse
		reason = "CredentialsMissing"
		modelConfig.Status.Phase = "Error"
	}

	setCondition(&modelConfig.Status.Conditions, metav1.Condition{
		Type:               "CredentialsValid",
		Status:             conditionStatus,
		ObservedGeneration: modelConfig.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            credentialMessage,
	})

	if err := r.Status().Update(ctx, &modelConfig); err != nil {
		logger.Error(err, "unable to update ModelConfig status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&modelConfig, "Normal", "Reconciled", "ModelConfig reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *ModelConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.ModelConfig{}).
		Complete(r)
}
