package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

// +kubebuilder:rbac:groups=karo.dev,resources=agentteams,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentteams/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentteams/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes,verbs=get;list;watch;create;update;patch

// AgentTeamReconciler reconciles a AgentTeam object
type AgentTeamReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentTeamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var team karov1alpha1.AgentTeam
	if err := r.Get(ctx, req.NamespacedName, &team); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch AgentTeam")
		return ctrl.Result{}, err
	}

	totalAgents := int32(len(team.Spec.Agents))
	readyAgents := int32(0)
	var degradedReasons []string

	// Validate all agentSpecRef entries exist and count ready agents
	for _, member := range team.Spec.Agents {
		var agentSpec karov1alpha1.AgentSpec
		key := types.NamespacedName{
			Name:      member.AgentSpecRef.Name,
			Namespace: team.Namespace,
		}
		if err := r.Get(ctx, key, &agentSpec); err != nil {
			degradedReasons = append(degradedReasons, fmt.Sprintf("AgentSpec %q not found", member.AgentSpecRef.Name))
			r.Recorder.Eventf(&team, corev1.EventTypeWarning, "AgentSpecNotFound",
				"Referenced AgentSpec %s not found", member.AgentSpecRef.Name)
			continue
		}

		if agentSpec.Status.Phase == PhaseReady {
			readyAgents++
		} else {
			degradedReasons = append(degradedReasons, fmt.Sprintf("AgentSpec %q is not Ready (phase: %s)", agentSpec.Name, agentSpec.Status.Phase))
		}

		// Ensure an AgentMailbox exists for this member
		if err := r.ensureMailboxForMember(ctx, &team, &member); err != nil {
			logger.Error(err, "failed to ensure mailbox for member", "agentSpec", member.AgentSpecRef.Name)
			degradedReasons = append(degradedReasons, fmt.Sprintf("failed to ensure mailbox for %q: %v", member.AgentSpecRef.Name, err))
		}
	}

	// Update status counts
	team.Status.TotalAgents = totalAgents
	team.Status.ReadyAgents = readyAgents

	// Determine phase
	switch {
	case readyAgents == 0 && totalAgents > 0:
		team.Status.Phase = PhaseDegraded
	case readyAgents < totalAgents:
		team.Status.Phase = "Partial"
	default:
		team.Status.Phase = PhaseReady
	}

	// Set AllAgentsReady condition
	if readyAgents == totalAgents && len(degradedReasons) == 0 {
		condition := metav1.Condition{
			Type:               "AllAgentsReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: team.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AllReady",
			Message:            fmt.Sprintf("All %d agents are ready", totalAgents),
		}
		setCondition(&team.Status.Conditions, condition)
	} else {
		condition := metav1.Condition{
			Type:               "AllAgentsReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: team.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "NotAllReady",
			Message:            fmt.Sprintf("%d/%d agents ready: %v", readyAgents, totalAgents, degradedReasons),
		}
		setCondition(&team.Status.Conditions, condition)
	}

	if err := r.Status().Update(ctx, &team); err != nil {
		logger.Error(err, "unable to update AgentTeam status")
		return ctrl.Result{}, err
	}

	logger.Info("reconciled AgentTeam", "phase", team.Status.Phase, "ready", readyAgents, "total", totalAgents)
	return ctrl.Result{}, nil
}

// ensureMailboxForMember ensures an AgentMailbox exists for the given team member.
// If one already exists, it is left unchanged. If not, a new one is created.
func (r *AgentTeamReconciler) ensureMailboxForMember(ctx context.Context, team *karov1alpha1.AgentTeam, member *karov1alpha1.AgentTeamMember) error {
	mailboxName := fmt.Sprintf("%s-%s-mailbox", team.Name, member.AgentSpecRef.Name)

	var existingMailbox karov1alpha1.AgentMailbox
	key := types.NamespacedName{Name: mailboxName, Namespace: team.Namespace}
	if err := r.Get(ctx, key, &existingMailbox); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		// Create the mailbox
		mailbox := &karov1alpha1.AgentMailbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mailboxName,
				Namespace: team.Namespace,
				Labels: map[string]string{
					"karo.dev/agent-team": team.Name,
					"karo.dev/agent-spec": member.AgentSpecRef.Name,
				},
			},
			Spec: karov1alpha1.AgentMailboxSpec{
				AgentSpecRef: member.AgentSpecRef,
				AcceptedMessageTypes: []karov1alpha1.MessageType{
					karov1alpha1.MessageTypeTaskAssigned,
					karov1alpha1.MessageTypeTaskDepUnblocked,
					karov1alpha1.MessageTypeAgentToAgent,
				},
				MaxPendingMessages: 100,
				Delivery: karov1alpha1.DeliveryConfig{
					Type: "pull",
				},
			},
		}

		// Set owner reference so mailbox is cleaned up with the team
		if err := ctrl.SetControllerReference(team, mailbox, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on mailbox: %w", err)
		}

		if err := r.Create(ctx, mailbox); err != nil {
			if errors.IsAlreadyExists(err) {
				return nil
			}
			return fmt.Errorf("failed to create mailbox: %w", err)
		}

		r.Recorder.Eventf(team, corev1.EventTypeNormal, "MailboxCreated",
			"Created AgentMailbox %s for member %s", mailboxName, member.AgentSpecRef.Name)
	}

	return nil
}

func (r *AgentTeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentTeam{}).
		Owns(&karov1alpha1.AgentMailbox{}).
		Complete(r)
}
