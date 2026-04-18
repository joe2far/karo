package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

// +kubebuilder:rbac:groups=karo.dev,resources=dispatchers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=dispatchers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=dispatchers/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=taskgraphs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// DispatcherReconciler reconciles a Dispatcher object.
type DispatcherReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *DispatcherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var dispatcher karov1alpha1.Dispatcher
	if err := r.Get(ctx, req.NamespacedName, &dispatcher); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// List TaskGraphs matching taskGraphSelector.
	selector, err := metav1.LabelSelectorAsSelector(&dispatcher.Spec.TaskGraphSelector)
	if err != nil {
		logger.Error(err, "invalid taskGraphSelector")
		return ctrl.Result{}, err
	}

	var taskGraphs karov1alpha1.TaskGraphList
	if err := r.List(ctx, &taskGraphs, &client.ListOptions{
		LabelSelector: selector,
		Namespace:     dispatcher.Namespace,
	}); err != nil {
		return ctrl.Result{}, err
	}

	var pendingTasks int32
	var totalDispatched int64

	// Count currently in-flight tasks across all TaskGraphs for maxConcurrent enforcement.
	var inFlight int32
	for i := range taskGraphs.Items {
		for _, ts := range taskGraphs.Items[i].Status.TaskStatuses {
			if ts.Phase == karov1alpha1.TaskPhaseDispatched || ts.Phase == karov1alpha1.TaskPhaseInProgress {
				inFlight++
			}
		}
	}

	for i := range taskGraphs.Items {
		tg := &taskGraphs.Items[i]
		if tg.Status.TaskStatuses == nil {
			continue
		}

		// Build task lookup.
		taskByID := make(map[string]karov1alpha1.Task, len(tg.Spec.Tasks))
		for _, t := range tg.Spec.Tasks {
			taskByID[t.ID] = t
		}

		for taskID, ts := range tg.Status.TaskStatuses {
			if ts.Phase != karov1alpha1.TaskPhaseOpen {
				if ts.Phase == karov1alpha1.TaskPhaseDispatched || ts.Phase == karov1alpha1.TaskPhaseInProgress {
					totalDispatched++
				}
				continue
			}

			pendingTasks++

			// Enforce maxConcurrent limit from the TaskGraph's dispatch policy.
			if tg.Spec.DispatchPolicy.MaxConcurrent > 0 && inFlight >= tg.Spec.DispatchPolicy.MaxConcurrent {
				continue
			}

			task, ok := taskByID[taskID]
			if !ok {
				continue
			}

			// Route by capability mode: match task.Type to capabilityRoutes.
			agentSpecName, skillPrompt := r.routeTask(&dispatcher, task)
			if agentSpecName == "" {
				logger.Info("no route found for task", "taskID", taskID, "taskType", task.Type)
				continue
			}

			// Look up the AgentSpec to check scaling limits and resolve skill prompt.
			var agentSpec karov1alpha1.AgentSpec
			agentSpecKey := types.NamespacedName{Name: agentSpecName, Namespace: dispatcher.Namespace}
			if err := r.Get(ctx, agentSpecKey, &agentSpec); err != nil {
				logger.Error(err, "failed to get AgentSpec for routing", "agentSpec", agentSpecName)
				continue
			}

			// Resolve SkillPrompt from AgentSpec capabilities for the matched task type.
			for _, cap := range agentSpec.Spec.Capabilities {
				if cap.Name == string(task.Type) && cap.SkillPrompt != nil && cap.SkillPrompt.Inline != "" {
					skillPrompt = cap.SkillPrompt.Inline
					break
				}
			}

			// Find or create an AgentMailbox for this agent.
			mailboxName := fmt.Sprintf("%s-mailbox", agentSpecName)
			var mailbox karov1alpha1.AgentMailbox
			mailboxKey := types.NamespacedName{Name: mailboxName, Namespace: dispatcher.Namespace}
			if err := r.Get(ctx, mailboxKey, &mailbox); err != nil {
				logger.Info("mailbox not found, skipping dispatch", "mailbox", mailboxName, "taskID", taskID)
				continue
			}

			// Check if we need to create an AgentInstance (no idle instance and under maxInstances).
			r.ensureAgentInstance(ctx, &dispatcher, &agentSpec, tg, task)

			// Deliver task to mailbox with skill prompt from routing.
			if err := r.deliverTask(ctx, &mailbox, tg, task, skillPrompt); err != nil {
				logger.Error(err, "failed to deliver task to mailbox", "taskID", taskID)
				continue
			}

			// Update task status to Dispatched in the TaskGraph.
			ts.Phase = karov1alpha1.TaskPhaseDispatched
			ts.AssignedTo = agentSpecName
			now := metav1.Now()
			ts.AssignedAt = &now
			tg.Status.TaskStatuses[taskID] = ts
			totalDispatched++
			inFlight++
			pendingTasks--

			r.Recorder.Event(&dispatcher, "Normal", "TaskDispatched",
				fmt.Sprintf("Task %s dispatched to %s", taskID, agentSpecName))
		}

		// Update TaskGraph status after dispatching.
		tg.Status.LastDispatchedAt = func() *metav1.Time { t := metav1.Now(); return &t }()
		if err := r.Status().Update(ctx, tg); err != nil {
			logger.Error(err, "failed to update TaskGraph status", "taskGraph", tg.Name)
		}
	}

	// Update Dispatcher status.
	dispatcher.Status.Phase = PhaseActive
	dispatcher.Status.PendingTasks = pendingTasks
	dispatcher.Status.TotalDispatched = totalDispatched
	now := metav1.Now()
	dispatcher.Status.LastDispatchedAt = &now

	setCondition(&dispatcher.Status.Conditions, metav1.Condition{
		Type:               PhaseReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: dispatcher.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "Reconciled",
		Message:            "Dispatcher is active and processing tasks",
	})

	if err := r.Status().Update(ctx, &dispatcher); err != nil {
		logger.Error(err, "unable to update Dispatcher status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// routeTask finds the AgentSpec name for a task based on capability routing.
// It also resolves the SkillPrompt from the matched AgentSpec capability.
func (r *DispatcherReconciler) routeTask(dispatcher *karov1alpha1.Dispatcher, task karov1alpha1.Task) (string, string) {
	switch dispatcher.Spec.Mode {
	case karov1alpha1.DispatchModeCapability:
		for _, route := range dispatcher.Spec.CapabilityRoutes {
			if route.Capability == string(task.Type) {
				return route.AgentSpecRef.Name, ""
			}
		}
		if dispatcher.Spec.FallbackAgentSpecRef != nil {
			return dispatcher.Spec.FallbackAgentSpecRef.Name, ""
		}
	case karov1alpha1.DispatchModeRoundRobin:
		if len(dispatcher.Spec.CapabilityRoutes) > 0 {
			return dispatcher.Spec.CapabilityRoutes[0].AgentSpecRef.Name, ""
		}
		if dispatcher.Spec.FallbackAgentSpecRef != nil {
			return dispatcher.Spec.FallbackAgentSpecRef.Name, ""
		}
	default:
		if dispatcher.Spec.FallbackAgentSpecRef != nil {
			return dispatcher.Spec.FallbackAgentSpecRef.Name, ""
		}
	}
	return "", ""
}

// ensureAgentInstance creates an AgentInstance if no idle instance exists
// and we are under the maxInstances limit for the AgentSpec.
func (r *DispatcherReconciler) ensureAgentInstance(ctx context.Context, dispatcher *karov1alpha1.Dispatcher, agentSpec *karov1alpha1.AgentSpec, tg *karov1alpha1.TaskGraph, task karov1alpha1.Task) {
	logger := log.FromContext(ctx)

	// List existing AgentInstances for this AgentSpec.
	var instances karov1alpha1.AgentInstanceList
	if err := r.List(ctx, &instances, &client.ListOptions{
		Namespace: dispatcher.Namespace,
	}); err != nil {
		logger.Error(err, "failed to list AgentInstances")
		return
	}

	// Count instances belonging to this AgentSpec and check for idle ones.
	var count int32
	hasIdle := false
	for _, inst := range instances.Items {
		if inst.Spec.AgentSpecRef.Name == agentSpec.Name {
			count++
			if inst.Status.Phase == karov1alpha1.AgentInstancePhaseIdle {
				hasIdle = true
			}
		}
	}

	// If there is an idle instance or we are at maxInstances, skip creation.
	if hasIdle || count >= agentSpec.Spec.Scaling.MaxInstances {
		return
	}

	// Create a new AgentInstance.
	instance := &karov1alpha1.AgentInstance{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", agentSpec.Name),
			Namespace:    dispatcher.Namespace,
			Labels: map[string]string{
				"karo.dev/agentspec":  agentSpec.Name,
				"karo.dev/dispatcher": dispatcher.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: dispatcher.APIVersion,
					Kind:       dispatcher.Kind,
					Name:       dispatcher.Name,
					UID:        dispatcher.UID,
				},
			},
		},
		Spec: karov1alpha1.AgentInstanceSpec{
			AgentSpecRef: corev1.LocalObjectReference{Name: agentSpec.Name},
			CurrentTaskRef: &karov1alpha1.TaskRef{
				TaskGraph: tg.Name,
				TaskID:    task.ID,
			},
		},
	}

	if err := r.Create(ctx, instance); err != nil {
		logger.Error(err, "failed to create AgentInstance", "agentSpec", agentSpec.Name)
		return
	}

	r.Recorder.Event(dispatcher, "Normal", "AgentInstanceCreated",
		fmt.Sprintf("Created AgentInstance for %s to handle task %s", agentSpec.Name, task.ID))
}

// deliverTask appends a TaskAssigned message to the AgentMailbox's pending messages.
func (r *DispatcherReconciler) deliverTask(ctx context.Context, mailbox *karov1alpha1.AgentMailbox, tg *karov1alpha1.TaskGraph, task karov1alpha1.Task, skillPrompt string) error {
	// Check max pending messages (default 100).
	maxPending := mailbox.Spec.MaxPendingMessages
	if maxPending == 0 {
		maxPending = 100
	}
	if mailbox.Status.PendingCount >= maxPending {
		return fmt.Errorf("mailbox %s is full (%d/%d)", mailbox.Name, mailbox.Status.PendingCount, maxPending)
	}

	// Read prior failure notes from TaskGraph status if available.
	var priorFailureNotes string
	if ts, ok := tg.Status.TaskStatuses[task.ID]; ok {
		priorFailureNotes = ts.FailureNotes
	}

	payload := karov1alpha1.TaskAssignedPayload{
		TaskGraphRef:       corev1.LocalObjectReference{Name: tg.Name},
		TaskID:             task.ID,
		TaskTitle:          task.Title,
		TaskType:           task.Type,
		TaskDescription:    task.Description,
		AcceptanceCriteria: task.AcceptanceCriteria,
		EvalGateEnabled:    task.EvalGate != nil,
		Priority:           task.Priority,
		PriorFailureNotes:  priorFailureNotes,
		SkillPrompt:        skillPrompt,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal task payload: %w", err)
	}

	rawPayload := &runtime.RawExtension{Raw: payloadBytes}
	msg := karov1alpha1.MailboxMessage{
		MessageType: karov1alpha1.MessageTypeTaskAssigned,
		MessageID:   generateMessageID(task.ID),
		Timestamp:   metav1.Now(),
		Payload:     rawPayload,
	}

	mailbox.Status.PendingMessages = append(mailbox.Status.PendingMessages, msg)
	mailbox.Status.PendingCount = int32(len(mailbox.Status.PendingMessages))
	mailbox.Status.TotalReceived++

	if err := r.Status().Update(ctx, mailbox); err != nil {
		return fmt.Errorf("failed to update mailbox status: %w", err)
	}

	return nil
}

// generateMessageID creates a unique message ID for a task.
func generateMessageID(taskID string) string {
	return fmt.Sprintf("msg-%s-%d", taskID, time.Now().UnixNano())
}

// findDispatcherForTaskGraph maps a TaskGraph event to the Dispatchers that
// should be reconciled. It lists all Dispatchers in the same namespace and
// checks if their taskGraphSelector matches the TaskGraph's labels.
func (r *DispatcherReconciler) findDispatcherForTaskGraph(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	tg, ok := obj.(*karov1alpha1.TaskGraph)
	if !ok {
		return nil
	}

	var dispatchers karov1alpha1.DispatcherList
	if err := r.List(ctx, &dispatchers, &client.ListOptions{
		Namespace: tg.Namespace,
	}); err != nil {
		logger.Error(err, "failed to list dispatchers")
		return nil
	}

	var requests []reconcile.Request
	for _, d := range dispatchers.Items {
		selector, err := metav1.LabelSelectorAsSelector(&d.Spec.TaskGraphSelector)
		if err != nil {
			continue
		}
		if selector.Matches(labels.Set(tg.Labels)) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      d.Name,
					Namespace: d.Namespace,
				},
			})
		}
	}
	return requests
}

func (r *DispatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.Dispatcher{}).
		Watches(&karov1alpha1.TaskGraph{},
			handler.EnqueueRequestsFromMapFunc(r.findDispatcherForTaskGraph)).
		Complete(r)
}
