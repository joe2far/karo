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

	karov1alpha1 "github.com/karo-dev/karo/api/v1alpha1"
)

// +kubebuilder:rbac:groups=karo.dev,resources=agentchannels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentchannels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentchannels/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// AgentChannelReconciler reconciles an AgentChannel object.
type AgentChannelReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var channel karov1alpha1.AgentChannel
	if err := r.Get(ctx, req.NamespacedName, &channel); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate platform credentials by checking that referenced secrets exist.
	credentialsValid, credMsg := r.validatePlatformCredentials(ctx, &channel)

	if !credentialsValid {
		channel.Status.Phase = "Error"
		channel.Status.PlatformConnected = false
		setCondition(&channel.Status.Conditions, metav1.Condition{
			Type:               "PlatformConnected",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: channel.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "CredentialsMissing",
			Message:            credMsg,
		})
		if err := r.Status().Update(ctx, &channel); err != nil {
			logger.Error(err, "unable to update AgentChannel status")
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&channel, "Warning", "CredentialsMissing", credMsg)
		return ctrl.Result{}, nil
	}

	// All credentials validated -- set phase to Active.
	channel.Status.Phase = "Active"
	channel.Status.PlatformConnected = true

	setCondition(&channel.Status.Conditions, metav1.Condition{
		Type:               "PlatformConnected",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: channel.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Connected",
		Message:            fmt.Sprintf("Platform %s credentials validated", channel.Spec.Platform.Type),
	})

	// Scan for approval tasks (type: approval) that need human interaction.
	r.handleApprovalTasks(ctx, &channel)

	setCondition(&channel.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: channel.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Reconciled",
		Message:            "AgentChannel is active",
	})

	if err := r.Status().Update(ctx, &channel); err != nil {
		logger.Error(err, "unable to update AgentChannel status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&channel, "Normal", "Reconciled", "AgentChannel reconciled successfully")
	return ctrl.Result{}, nil
}

// validatePlatformCredentials checks that the secrets referenced by the
// platform configuration exist in the channel's namespace.
func (r *AgentChannelReconciler) validatePlatformCredentials(ctx context.Context, channel *karov1alpha1.AgentChannel) (bool, string) {
	platform := channel.Spec.Platform

	switch platform.Type {
	case karov1alpha1.ChannelPlatformSlack:
		if platform.Slack == nil {
			return false, "slack platform config is nil"
		}
		if err := r.checkSecretExists(ctx, channel.Namespace, platform.Slack.AppCredentialSecret.Name); err != nil {
			return false, fmt.Sprintf("slack appCredentialSecret not found: %v", err)
		}
		if err := r.checkSecretExists(ctx, channel.Namespace, platform.Slack.SigningSecret.Name); err != nil {
			return false, fmt.Sprintf("slack signingSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformTelegram:
		if platform.Telegram == nil {
			return false, "telegram platform config is nil"
		}
		if err := r.checkSecretExists(ctx, channel.Namespace, platform.Telegram.BotTokenSecret.Name); err != nil {
			return false, fmt.Sprintf("telegram botTokenSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformDiscord:
		if platform.Discord == nil {
			return false, "discord platform config is nil"
		}
		if err := r.checkSecretExists(ctx, channel.Namespace, platform.Discord.BotTokenSecret.Name); err != nil {
			return false, fmt.Sprintf("discord botTokenSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformTeams:
		if platform.Teams == nil {
			return false, "teams platform config is nil"
		}
		if err := r.checkSecretExists(ctx, channel.Namespace, platform.Teams.AppCredentialSecret.Name); err != nil {
			return false, fmt.Sprintf("teams appCredentialSecret not found: %v", err)
		}

	case karov1alpha1.ChannelPlatformWebhook:
		if platform.Webhook == nil {
			return false, "webhook platform config is nil"
		}
		if platform.Webhook.AuthSecret != nil {
			if err := r.checkSecretExists(ctx, channel.Namespace, platform.Webhook.AuthSecret.Name); err != nil {
				return false, fmt.Sprintf("webhook authSecret not found: %v", err)
			}
		}

	default:
		return false, fmt.Sprintf("unsupported platform type: %s", platform.Type)
	}

	return true, ""
}

// checkSecretExists verifies that a Secret with the given name exists in the namespace.
func (r *AgentChannelReconciler) checkSecretExists(ctx context.Context, namespace, name string) error {
	var secret corev1.Secret
	key := types.NamespacedName{Name: name, Namespace: namespace}
	if err := r.Get(ctx, key, &secret); err != nil {
		return fmt.Errorf("secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// handleApprovalTasks scans TaskGraphs in the channel's namespace for tasks
// with type "approval" that are Open and transitions them to AwaitingApproval.
// In a full implementation, this would also post the approval request to the
// configured platform (Slack, Telegram, etc.).
func (r *AgentChannelReconciler) handleApprovalTasks(ctx context.Context, channel *karov1alpha1.AgentChannel) {
	logger := log.FromContext(ctx)

	var taskGraphs karov1alpha1.TaskGraphList
	if err := r.List(ctx, &taskGraphs, client.InNamespace(channel.Namespace)); err != nil {
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
				// Transition to AwaitingApproval — the channel owns this task now.
				ts.Phase = karov1alpha1.TaskPhaseAwaitingApproval
				now := metav1.Now()
				ts.AssignedAt = &now
				ts.AssignedTo = fmt.Sprintf("channel:%s", channel.Name)
				tg.Status.TaskStatuses[task.ID] = ts
				updated = true
				pendingApprovals++
				r.Recorder.Eventf(channel, "Normal", "ApprovalRequested",
					"Approval task %s from TaskGraph %s posted to %s channel",
					task.ID, tg.Name, channel.Spec.Platform.Type)
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

	channel.Status.PendingApprovals = pendingApprovals
}

func (r *AgentChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentChannel{}).
		Complete(r)
}
