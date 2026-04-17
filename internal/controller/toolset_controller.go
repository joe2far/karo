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
	"github.com/joe2far/karo/internal/gateway"
)

const toolSetFinalizer = "karo.dev/toolset-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=toolsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=agentgateway.dev,resources=agentgatewaybackends,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete

type ToolSetReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	GatewayTranslator *gateway.Translator
}

func (r *ToolSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var toolSet karov1alpha1.ToolSet
	if err := r.Get(ctx, req.NamespacedName, &toolSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion.
	if !toolSet.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&toolSet, toolSetFinalizer) {
			controllerutil.RemoveFinalizer(&toolSet, toolSetFinalizer)
			if err := r.Update(ctx, &toolSet); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&toolSet, toolSetFinalizer) {
		controllerutil.AddFinalizer(&toolSet, toolSetFinalizer)
		if err := r.Update(ctx, &toolSet); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate each tool entry.
	toolCount := int32(len(toolSet.Spec.Tools))
	var reachableCount int32
	var validationErrors []string

	for _, tool := range toolSet.Spec.Tools {
		valid := true

		// Validate transport configuration.
		switch tool.Transport {
		case karov1alpha1.MCPTransportStdio:
			if len(tool.Command) == 0 {
				validationErrors = append(validationErrors, fmt.Sprintf("tool %q: stdio transport requires a command", tool.Name))
				valid = false
			}
		case karov1alpha1.MCPTransportSSE, karov1alpha1.MCPTransportStreamableHTTP:
			if tool.Endpoint == "" {
				validationErrors = append(validationErrors, fmt.Sprintf("tool %q: %s transport requires an endpoint", tool.Name, tool.Transport))
				valid = false
			}
		default:
			if tool.Transport != "" {
				validationErrors = append(validationErrors, fmt.Sprintf("tool %q: unsupported transport %q", tool.Name, tool.Transport))
				valid = false
			}
		}

		// Validate credential secret exists if referenced.
		if tool.CredentialSecret != nil {
			secretKey := types.NamespacedName{
				Name:      tool.CredentialSecret.Name,
				Namespace: toolSet.Namespace,
			}
			var secret corev1.Secret
			if err := r.Get(ctx, secretKey, &secret); err != nil {
				validationErrors = append(validationErrors, fmt.Sprintf("tool %q: credential secret %q not found", tool.Name, tool.CredentialSecret.Name))
				valid = false
			} else if tool.CredentialSecret.Key != "" {
				if _, exists := secret.Data[tool.CredentialSecret.Key]; !exists {
					validationErrors = append(validationErrors, fmt.Sprintf("tool %q: key %q not found in secret %q", tool.Name, tool.CredentialSecret.Key, tool.CredentialSecret.Name))
					valid = false
				}
			}
		}

		if valid {
			reachableCount++
		}
	}

	// Validate policyRef if set.
	if toolSet.Spec.PolicyRef != nil {
		var policy karov1alpha1.AgentPolicy
		policyKey := types.NamespacedName{
			Name:      toolSet.Spec.PolicyRef.Name,
			Namespace: toolSet.Namespace,
		}
		if err := r.Get(ctx, policyKey, &policy); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("policyRef %q not found", toolSet.Spec.PolicyRef.Name))
			r.Recorder.Eventf(&toolSet, corev1.EventTypeWarning, "PolicyNotFound",
				"Referenced AgentPolicy %s not found", toolSet.Spec.PolicyRef.Name)
		}
	}

	// Update status.
	toolSet.Status.AvailableTools = reachableCount

	if len(validationErrors) > 0 && reachableCount < toolCount {
		toolSet.Status.Phase = "Degraded"
		setCondition(&toolSet.Status.Conditions, metav1.Condition{
			Type:               "AllToolsReachable",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: toolSet.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ValidationErrors",
			Message:            fmt.Sprintf("%d/%d tools reachable: %v", reachableCount, toolCount, validationErrors),
		})
	} else {
		toolSet.Status.Phase = "Ready"
		setCondition(&toolSet.Status.Conditions, metav1.Condition{
			Type:               "AllToolsReachable",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: toolSet.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ToolsValidated",
			Message:            fmt.Sprintf("All %d tools validated and reachable", toolCount),
		})
	}

	// Gateway delegation — if spec.gatewayRef is set, translate tools into
	// native agentgateway.dev resources so agents reach MCP tools through
	// the gateway's proxy plane rather than dialing each endpoint directly.
	r.reconcileGateway(ctx, &toolSet)

	if err := r.Status().Update(ctx, &toolSet); err != nil {
		logger.Error(err, "unable to update ToolSet status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&toolSet, "Normal", "Reconciled", fmt.Sprintf("ToolSet reconciled with %d/%d tools available", reachableCount, toolCount))
	return ctrl.Result{}, nil
}

// reconcileGateway handles optional agentgateway.dev delegation for MCP
// tools. On success, ToolSet.status gets a `GatewayWired` condition and the
// gateway-facing endpoint is reflected in the status message.
func (r *ToolSetReconciler) reconcileGateway(ctx context.Context, ts *karov1alpha1.ToolSet) {
	logger := log.FromContext(ctx)

	if r.GatewayTranslator == nil {
		return
	}

	if ts.Spec.GatewayRef == nil {
		if err := r.GatewayTranslator.CleanupToolSetResources(ctx, ts); err != nil {
			logger.Error(err, "failed to clean up gateway resources")
		}
		removeCondition(&ts.Status.Conditions, "GatewayWired")
		return
	}

	endpoint, err := r.GatewayTranslator.EnsureToolSetResources(ctx, ts)
	if err != nil {
		ts.Status.Phase = "Degraded"
		setCondition(&ts.Status.Conditions, metav1.Condition{
			Type:               "GatewayWired",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: ts.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "TranslationFailed",
			Message:            fmt.Sprintf("failed to render gateway resources: %v", err),
		})
		r.Recorder.Event(ts, corev1.EventTypeWarning, "GatewayTranslationFailed", err.Error())
		return
	}
	setCondition(&ts.Status.Conditions, metav1.Condition{
		Type:               "GatewayWired",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: ts.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "ResourcesApplied",
		Message:            fmt.Sprintf("AgentgatewayBackends and HTTPRoute applied; endpoint=%s", endpoint),
	})
}

func (r *ToolSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.ToolSet{}).
		Complete(r)
}
