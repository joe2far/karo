package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	"github.com/joe2far/karo/internal/gateway"
)

const modelConfigFinalizer = "karo.dev/modelconfig-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=agentgateway.dev,resources=agentgatewaybackends,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete

type ModelConfigReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	GatewayTranslator *gateway.Translator
}

func (r *ModelConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var modelConfig karov1alpha1.ModelConfig
	if err := r.Get(ctx, req.NamespacedName, &modelConfig); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion — explicitly clean up generated agentgateway
	// resources before removing the finalizer. OwnerReferences would
	// normally cascade-delete, but running cleanup here is defence in
	// depth and matches the behaviour for `gatewayRef` clearing.
	if !modelConfig.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&modelConfig, modelConfigFinalizer) {
			if r.GatewayTranslator != nil {
				if err := r.GatewayTranslator.CleanupModelConfigResources(ctx, &modelConfig); err != nil {
					logger.Error(err, "failed to clean up gateway resources on delete")
					return ctrl.Result{}, err
				}
			}
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
		modelConfig.Status.Phase = PhaseError
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
	modelConfig.Status.Phase = PhaseReady
	modelConfig.Status.Provider = modelConfig.Spec.Provider
	now := metav1.Now()
	modelConfig.Status.LastValidatedAt = &now

	conditionStatus := metav1.ConditionTrue
	reason := "CredentialsFound"
	if !credentialsValid {
		conditionStatus = metav1.ConditionFalse
		reason = "CredentialsMissing"
		modelConfig.Status.Phase = PhaseError
	}

	setCondition(&modelConfig.Status.Conditions, metav1.Condition{
		Type:               "CredentialsValid",
		Status:             conditionStatus,
		ObservedGeneration: modelConfig.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            credentialMessage,
	})

	// Gateway delegation — if the user set spec.gatewayRef, materialize the
	// native agentgateway.dev resources (AgentgatewayBackend + HTTPRoute)
	// and publish the gateway-facing endpoint on status.resolvedEndpoint.
	r.reconcileGateway(ctx, &modelConfig)

	if err := r.Status().Update(ctx, &modelConfig); err != nil {
		logger.Error(err, "unable to update ModelConfig status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&modelConfig, "Normal", "Reconciled", "ModelConfig reconciled successfully")
	return ctrl.Result{}, nil
}

// reconcileGateway handles the optional agentgateway.dev delegation. It
// short-circuits when neither the translator nor a gatewayRef are set, emits
// a status condition describing the wiring state, and writes the resolved
// endpoint to status for agents to consume.
func (r *ModelConfigReconciler) reconcileGateway(ctx context.Context, mc *karov1alpha1.ModelConfig) {
	logger := log.FromContext(ctx)

	if r.GatewayTranslator == nil {
		return
	}

	// gatewayRef cleared — clean up any previously generated resources.
	// Only zero the ResolvedEndpoint if this controller previously owned
	// it (evidenced by a GatewayWired condition); otherwise we'd clobber
	// an endpoint set by some other code path.
	if mc.Spec.GatewayRef == nil {
		if err := r.GatewayTranslator.CleanupModelConfigResources(ctx, mc); err != nil {
			logger.Error(err, "failed to clean up gateway resources")
		}
		if hasCondition(mc.Status.Conditions, "GatewayWired") {
			mc.Status.ResolvedEndpoint = ""
		}
		removeCondition(&mc.Status.Conditions, "GatewayWired")
		return
	}

	endpoint, err := r.GatewayTranslator.EnsureModelConfigResources(ctx, mc)
	if err != nil {
		mc.Status.Phase = PhaseDegraded
		setCondition(&mc.Status.Conditions, metav1.Condition{
			Type:               "GatewayWired",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: mc.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "TranslationFailed",
			Message:            fmt.Sprintf("failed to render gateway resources: %v", err),
		})
		r.Recorder.Event(mc, corev1.EventTypeWarning, "GatewayTranslationFailed", err.Error())
		return
	}

	mc.Status.ResolvedEndpoint = endpoint

	// For Bedrock/Vertex the gateway's own ServiceAccount needs IRSA / GKE
	// Workload Identity bindings. KARO cannot mutate a Gateway it does not
	// own, so surface the required annotations as an informational message
	// and set the condition to True with reason explaining the caveat.
	msg := fmt.Sprintf("AgentgatewayBackend and HTTPRoute applied; endpoint=%s", endpoint)
	if annots := gateway.RequiredServiceAccountAnnotations(mc); len(annots) > 0 {
		var pairs []string
		for k, v := range annots {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
		msg = fmt.Sprintf("%s. Gateway Pod ServiceAccount must carry annotations: %s",
			msg, joinComma(pairs))
	}

	setCondition(&mc.Status.Conditions, metav1.Condition{
		Type:               "GatewayWired",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: mc.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "ResourcesApplied",
		Message:            msg,
	})
}

// joinComma is a comma-separating helper that tolerates a nil slice.
func joinComma(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// removeCondition removes the condition with the given Type, if present.
func removeCondition(conditions *[]metav1.Condition, t string) {
	if conditions == nil {
		return
	}
	filtered := (*conditions)[:0]
	for _, c := range *conditions {
		if c.Type != t {
			filtered = append(filtered, c)
		}
	}
	*conditions = filtered
}

// hasCondition reports whether the conditions slice carries a condition of
// the given Type, regardless of its Status.
func hasCondition(conditions []metav1.Condition, t string) bool {
	for _, c := range conditions {
		if c.Type == t {
			return true
		}
	}
	return false
}

func (r *ModelConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.ModelConfig{}).
		Complete(r)
}
