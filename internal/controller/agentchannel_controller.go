package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
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
	"github.com/joe2far/karo/internal/channel"
)

const agentChannelFinalizer = "karo.dev/agentchannel-finalizer"

// +kubebuilder:rbac:groups=karo.dev,resources=agentchannels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentchannels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentchannels/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// AgentChannelReconciler reconciles an AgentChannel object.
type AgentChannelReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	GatewayManager *channel.GatewayManager
}

func (r *AgentChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ch karov1alpha1.AgentChannel
	if err := r.Get(ctx, req.NamespacedName, &ch); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion — clean up gateway resources.
	if !ch.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&ch, agentChannelFinalizer) {
			if r.GatewayManager != nil {
				if err := r.GatewayManager.CleanupGateway(ctx, &ch); err != nil {
					logger.Error(err, "failed to cleanup gateway on deletion")
				}
			}
			controllerutil.RemoveFinalizer(&ch, agentChannelFinalizer)
			if err := r.Update(ctx, &ch); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(&ch, agentChannelFinalizer) {
		controllerutil.AddFinalizer(&ch, agentChannelFinalizer)
		if err := r.Update(ctx, &ch); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate platform credentials by checking that referenced secrets exist.
	credentialsValid, credMsg := r.validatePlatformCredentials(ctx, &ch)

	if !credentialsValid {
		ch.Status.Phase = "Error"
		ch.Status.PlatformConnected = false
		setCondition(&ch.Status.Conditions, metav1.Condition{
			Type:               "PlatformConnected",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: ch.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "CredentialsMissing",
			Message:            credMsg,
		})
		if err := r.Status().Update(ctx, &ch); err != nil {
			logger.Error(err, "unable to update AgentChannel status")
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&ch, "Warning", "CredentialsMissing", credMsg)
		return ctrl.Result{}, nil
	}

	// Ensure the gateway Deployment + Service exist.
	if r.GatewayManager != nil {
		if err := r.GatewayManager.EnsureGateway(ctx, &ch); err != nil {
			logger.Error(err, "failed to ensure channel gateway")
			ch.Status.Phase = "Error"
			setCondition(&ch.Status.Conditions, metav1.Condition{
				Type:               "GatewayReady",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: ch.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "GatewayFailed",
				Message:            fmt.Sprintf("Failed to create gateway: %v", err),
			})
			if statusErr := r.Status().Update(ctx, &ch); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, err
		}

		// Check if the gateway Deployment is ready.
		gwReady := r.isGatewayReady(ctx, &ch)
		if gwReady {
			setCondition(&ch.Status.Conditions, metav1.Condition{
				Type:               "GatewayReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: ch.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "GatewayRunning",
				Message:            fmt.Sprintf("Gateway endpoint: %s", r.GatewayManager.GetGatewayEndpoint(&ch)),
			})
		} else {
			setCondition(&ch.Status.Conditions, metav1.Condition{
				Type:               "GatewayReady",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: ch.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "GatewayPending",
				Message:            "Gateway deployment is not yet ready",
			})
		}
	}

	// All credentials validated — set phase to Active.
	ch.Status.Phase = "Active"
	ch.Status.PlatformConnected = true

	setCondition(&ch.Status.Conditions, metav1.Condition{
		Type:               "PlatformConnected",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: ch.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Connected",
		Message:            fmt.Sprintf("Platform %s credentials validated", ch.Spec.Platform.Type),
	})

	// Scan for approval tasks.
	r.handleApprovalTasks(ctx, &ch)

	setCondition(&ch.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: ch.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Reconciled",
		Message:            "AgentChannel is active",
	})

	if err := r.Status().Update(ctx, &ch); err != nil {
		logger.Error(err, "unable to update AgentChannel status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&ch, "Normal", "Reconciled", "AgentChannel reconciled successfully")
	return ctrl.Result{}, nil
}

// isGatewayReady checks if the gateway Deployment has available replicas.
func (r *AgentChannelReconciler) isGatewayReady(ctx context.Context, ch *karov1alpha1.AgentChannel) bool {
	deployName := fmt.Sprintf("karo-gw-%s", ch.Name)
	var deploy appsv1.Deployment
	key := types.NamespacedName{Name: deployName, Namespace: ch.Namespace}
	if err := r.Get(ctx, key, &deploy); err != nil {
		return false
	}
	return deploy.Status.AvailableReplicas > 0
}

func (r *AgentChannelReconciler) validatePlatformCredentials(ctx context.Context, ch *karov1alpha1.AgentChannel) (bool, string) {
	platform := ch.Spec.Platform

	switch platform.Type {
	case karov1alpha1.ChannelPlatformSlack:
		if platform.Slack == nil {
			return false, "slack platform config is nil"
		}
		if err := r.checkSecretExists(ctx, ch.Namespace, platform.Slack.AppCredentialSecret.Name); err != nil {
			return false, fmt.Sprintf("slack appCredentialSecret not found: %v", err)
		}
		if err := r.checkSecretExists(ctx, ch.Namespace, platform.Slack.SigningSecret.Name); err != nil {
			return false, fmt.Sprintf("slack signingSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformTelegram:
		if platform.Telegram == nil {
			return false, "telegram platform config is nil"
		}
		if err := r.checkSecretExists(ctx, ch.Namespace, platform.Telegram.BotTokenSecret.Name); err != nil {
			return false, fmt.Sprintf("telegram botTokenSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformDiscord:
		if platform.Discord == nil {
			return false, "discord platform config is nil"
		}
		if err := r.checkSecretExists(ctx, ch.Namespace, platform.Discord.BotTokenSecret.Name); err != nil {
			return false, fmt.Sprintf("discord botTokenSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformTeams:
		if platform.Teams == nil {
			return false, "teams platform config is nil"
		}
		if err := r.checkSecretExists(ctx, ch.Namespace, platform.Teams.AppCredentialSecret.Name); err != nil {
			return false, fmt.Sprintf("teams appCredentialSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformWebhook:
		if platform.Webhook == nil {
			return false, "webhook platform config is nil"
		}
		if platform.Webhook.AuthSecret != nil {
			if err := r.checkSecretExists(ctx, ch.Namespace, platform.Webhook.AuthSecret.Name); err != nil {
				return false, fmt.Sprintf("webhook authSecret not found: %v", err)
			}
		}

	default:
		return false, fmt.Sprintf("unsupported platform type: %s", platform.Type)
	}

	return true, ""
}

func (r *AgentChannelReconciler) checkSecretExists(ctx context.Context, namespace, name string) error {
	var secret corev1.Secret
	key := types.NamespacedName{Name: name, Namespace: namespace}
	if err := r.Get(ctx, key, &secret); err != nil {
		return fmt.Errorf("secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (r *AgentChannelReconciler) handleApprovalTasks(ctx context.Context, ch *karov1alpha1.AgentChannel) {
	logger := log.FromContext(ctx)

	var taskGraphs karov1alpha1.TaskGraphList
	if err := r.List(ctx, &taskGraphs, client.InNamespace(ch.Namespace)); err != nil {
		logger.Error(err, "failed to list TaskGraphs for approval handling")
		return
	}

	var pendingApprovals int32
	for i := range taskGraphs.Items {
		tg := &taskGraphs.Items[i]
		updated := false
		for _, task := range tg.Spec.Tasks {
			if task.Type != karov1alpha1.TaskTypeApproval {
				continue
			}
			ts, exists := tg.Status.TaskStatuses[task.ID]
			if !exists {
				continue
			}
			if ts.Phase == karov1alpha1.TaskPhaseOpen {
				ts.Phase = karov1alpha1.TaskPhaseAwaitingApproval
				now := metav1.Now()
				ts.AssignedAt = &now
				ts.AssignedTo = fmt.Sprintf("channel:%s", ch.Name)
				tg.Status.TaskStatuses[task.ID] = ts
				updated = true
				pendingApprovals++
				r.Recorder.Eventf(ch, "Normal", "ApprovalRequested",
					"Approval task %s from TaskGraph %s posted to %s channel",
					task.ID, tg.Name, ch.Spec.Platform.Type)
			} else if ts.Phase == karov1alpha1.TaskPhaseAwaitingApproval {
				pendingApprovals++
			}
		}
		if updated {
			if err := r.Status().Update(ctx, tg); err != nil {
				logger.Error(err, "failed to update TaskGraph for approval", "taskGraph", tg.Name)
			}
		}
	}

	ch.Status.PendingApprovals = pendingApprovals
}

func (r *AgentChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentChannel{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
