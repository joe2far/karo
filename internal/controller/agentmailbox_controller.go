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

// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=agentinstances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// AgentMailboxReconciler reconciles a AgentMailbox object
type AgentMailboxReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentMailboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var mailbox karov1alpha1.AgentMailbox
	if err := r.Get(ctx, req.NamespacedName, &mailbox); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch AgentMailbox")
		return ctrl.Result{}, err
	}

	// Validate agentSpecRef exists
	var agentSpec karov1alpha1.AgentSpec
	agentSpecKey := types.NamespacedName{
		Name:      mailbox.Spec.AgentSpecRef.Name,
		Namespace: mailbox.Namespace,
	}
	if err := r.Get(ctx, agentSpecKey, &agentSpec); err != nil {
		logger.Error(err, "referenced AgentSpec not found", "agentSpec", mailbox.Spec.AgentSpecRef.Name)
		r.Recorder.Eventf(&mailbox, corev1.EventTypeWarning, "AgentSpecNotFound",
			"Referenced AgentSpec %s not found", mailbox.Spec.AgentSpecRef.Name)

		mailbox.Status.Phase = "Degraded"
		condition := metav1.Condition{
			Type:               "Active",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: mailbox.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AgentSpecNotFound",
			Message:            fmt.Sprintf("Referenced AgentSpec %s not found", mailbox.Spec.AgentSpecRef.Name),
		}
		setCondition(&mailbox.Status.Conditions, condition)

		if err := r.Status().Update(ctx, &mailbox); err != nil {
			logger.Error(err, "unable to update AgentMailbox status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Set phase to Active
	mailbox.Status.Phase = "Active"

	// Recompute pending count
	r.recomputeMailboxStatus(&mailbox)

	// If there are pending messages, check for hibernated agents that should be woken.
	if mailbox.Status.PendingCount > 0 {
		r.wakeHibernatedAgents(ctx, &mailbox)
	}

	// Set Active condition
	activeCondition := metav1.Condition{
		Type:               "Active",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: mailbox.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "MailboxActive",
		Message:            fmt.Sprintf("AgentMailbox is active for AgentSpec %s", mailbox.Spec.AgentSpecRef.Name),
	}
	setCondition(&mailbox.Status.Conditions, activeCondition)

	if err := r.Status().Update(ctx, &mailbox); err != nil {
		logger.Error(err, "unable to update AgentMailbox status")
		return ctrl.Result{}, err
	}

	logger.Info("reconciled AgentMailbox", "phase", mailbox.Status.Phase, "pendingCount", mailbox.Status.PendingCount)
	return ctrl.Result{}, nil
}

func (r *AgentMailboxReconciler) acknowledgeMessage(ctx context.Context, mailbox *karov1alpha1.AgentMailbox, messageID string) error {
	filtered := make([]karov1alpha1.MailboxMessage, 0, len(mailbox.Status.PendingMessages))
	found := false
	for _, msg := range mailbox.Status.PendingMessages {
		if msg.MessageID == messageID {
			found = true
			mailbox.Status.TotalProcessed++
			r.Recorder.Eventf(mailbox, corev1.EventTypeNormal, "MessageProcessed",
				"Message %s (type: %s) acknowledged and removed", msg.MessageID, msg.MessageType)
			continue
		}
		filtered = append(filtered, msg)
	}
	if !found {
		return fmt.Errorf("message %s not found in mailbox %s", messageID, mailbox.Name)
	}
	mailbox.Status.PendingMessages = filtered
	r.recomputeMailboxStatus(mailbox)
	return r.Status().Update(ctx, mailbox)
}

func (r *AgentMailboxReconciler) recomputeMailboxStatus(mailbox *karov1alpha1.AgentMailbox) {
	mailbox.Status.PendingCount = int32(len(mailbox.Status.PendingMessages))
	if mailbox.Status.PendingCount > 0 {
		oldest := mailbox.Status.PendingMessages[0].Timestamp
		mailbox.Status.OldestPendingMessage = &oldest
	} else {
		mailbox.Status.OldestPendingMessage = nil
	}
}

// wakeHibernatedAgents finds hibernated AgentInstances for this mailbox's
// AgentSpec and transitions them to Pending so the AgentInstance controller
// will recreate the pod.
func (r *AgentMailboxReconciler) wakeHibernatedAgents(ctx context.Context, mailbox *karov1alpha1.AgentMailbox) {
	logger := log.FromContext(ctx)

	var instances karov1alpha1.AgentInstanceList
	if err := r.List(ctx, &instances, client.InNamespace(mailbox.Namespace)); err != nil {
		logger.Error(err, "failed to list AgentInstances for wake check")
		return
	}

	for i := range instances.Items {
		inst := &instances.Items[i]
		if inst.Spec.AgentSpecRef.Name != mailbox.Spec.AgentSpecRef.Name {
			continue
		}
		if inst.Status.Phase != karov1alpha1.AgentInstancePhaseHibernated {
			continue
		}
		if !inst.Spec.Hibernation.ResumeOnMail {
			continue
		}

		// Wake the agent by setting phase back to Pending.
		inst.Status.Phase = karov1alpha1.AgentInstancePhasePending
		if err := r.Status().Update(ctx, inst); err != nil {
			logger.Error(err, "failed to wake hibernated agent", "instance", inst.Name)
			continue
		}
		r.Recorder.Eventf(mailbox, corev1.EventTypeNormal, "AgentWoken",
			"Woke hibernated agent %s due to pending messages", inst.Name)
	}
}

func (r *AgentMailboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentMailbox{}).
		Complete(r)
}
