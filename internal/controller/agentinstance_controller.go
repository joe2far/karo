package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/karo-dev/karo/api/v1alpha1"
	gitinjector "github.com/karo-dev/karo/internal/git"
)

// +kubebuilder:rbac:groups=karo.dev,resources=agentinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=agentspecs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=agentteams,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=memorystores,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=sandboxclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=agentmailboxes/status,verbs=get
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete

// EffectiveBindings holds the resolved resource references for an AgentInstance,
// combining AgentSpec-level refs with AgentTeam shared resources as fallback.
type EffectiveBindings struct {
	ModelConfigRef  string
	MemoryRef       string
	ToolSetRef      string
	SandboxClassRef string
}

// AgentInstanceReconciler reconciles a AgentInstance object
type AgentInstanceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var instance karov1alpha1.AgentInstance
	if err := r.Get(ctx, req.NamespacedName, &instance); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch AgentInstance")
		return ctrl.Result{}, err
	}

	// If terminated, do nothing
	if instance.Status.Phase == karov1alpha1.AgentInstancePhaseTerminated {
		return ctrl.Result{}, nil
	}

	// Fetch the referenced AgentSpec
	var agentSpec karov1alpha1.AgentSpec
	agentSpecKey := types.NamespacedName{
		Name:      instance.Spec.AgentSpecRef.Name,
		Namespace: instance.Namespace,
	}
	if err := r.Get(ctx, agentSpecKey, &agentSpec); err != nil {
		logger.Error(err, "referenced AgentSpec not found", "agentSpec", instance.Spec.AgentSpecRef.Name)
		r.Recorder.Eventf(&instance, corev1.EventTypeWarning, "AgentSpecNotFound",
			"Referenced AgentSpec %s not found", instance.Spec.AgentSpecRef.Name)
		instance.Status.Phase = karov1alpha1.AgentInstancePhasePending
		condition := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: instance.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AgentSpecNotFound",
			Message:            fmt.Sprintf("Referenced AgentSpec %s not found", instance.Spec.AgentSpecRef.Name),
		}
		setCondition(&instance.Status.Conditions, condition)
		if err := r.Status().Update(ctx, &instance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Resolve effective bindings (AgentSpec + team fallback)
	bindings, err := r.resolveEffectiveBindings(ctx, &agentSpec)
	if err != nil {
		logger.Error(err, "failed to resolve effective bindings")
	}

	// OnDemand startPolicy: do not create a pod until there are pending mailbox messages.
	if agentSpec.Spec.Scaling.StartPolicy == karov1alpha1.StartPolicyOnDemand {
		mailboxName := fmt.Sprintf("%s-mailbox", agentSpec.Name)
		var mailbox karov1alpha1.AgentMailbox
		mailboxKey := types.NamespacedName{Name: mailboxName, Namespace: instance.Namespace}
		if err := r.Get(ctx, mailboxKey, &mailbox); err != nil {
			// If mailbox not found, treat as no messages.
			if instance.Status.Phase == "" || instance.Status.Phase == karov1alpha1.AgentInstancePhasePending {
				instance.Status.Phase = karov1alpha1.AgentInstancePhaseHibernated
				setCondition(&instance.Status.Conditions, metav1.Condition{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: instance.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             "OnDemandNoMailbox",
					Message:            "OnDemand: mailbox not found, hibernating",
				})
				if statusErr := r.Status().Update(ctx, &instance); statusErr != nil {
					return ctrl.Result{}, statusErr
				}
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
		} else if mailbox.Status.PendingCount == 0 {
			// No pending messages — hibernate if not already running.
			if instance.Status.Phase == "" || instance.Status.Phase == karov1alpha1.AgentInstancePhasePending {
				instance.Status.Phase = karov1alpha1.AgentInstancePhaseHibernated
				setCondition(&instance.Status.Conditions, metav1.Condition{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: instance.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             "OnDemandNoMessages",
					Message:            "OnDemand: no pending messages, hibernating",
				})
				if statusErr := r.Status().Update(ctx, &instance); statusErr != nil {
					return ctrl.Result{}, statusErr
				}
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
		}
	}

	// Build and ensure Pod exists
	podName := fmt.Sprintf("%s-pod", instance.Name)
	var existingPod corev1.Pod
	podKey := types.NamespacedName{Name: podName, Namespace: instance.Namespace}
	podExists := true
	if err := r.Get(ctx, podKey, &existingPod); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "unable to fetch Pod")
			return ctrl.Result{}, err
		}
		podExists = false
	}

	if !podExists {
		pod := r.buildPod(&instance, &agentSpec, podName, bindings)
		// Set owner reference
		if err := ctrl.SetControllerReference(&instance, pod, r.Scheme); err != nil {
			logger.Error(err, "unable to set controller reference on Pod")
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, pod); err != nil {
			if !errors.IsAlreadyExists(err) {
				logger.Error(err, "unable to create Pod")
				r.Recorder.Eventf(&instance, corev1.EventTypeWarning, "PodCreateFailed",
					"Failed to create pod: %v", err)
				return ctrl.Result{}, err
			}
		} else {
			r.Recorder.Eventf(&instance, corev1.EventTypeNormal, "PodCreated",
				"Created pod %s for agent instance", podName)
		}

		instance.Status.Phase = karov1alpha1.AgentInstancePhasePending
		now := metav1.Now()
		instance.Status.StartedAt = &now
		instance.Status.PodRef = &corev1.ObjectReference{
			Kind:      "Pod",
			Name:      podName,
			Namespace: instance.Namespace,
		}
	} else {
		// Update status based on pod phase
		r.updatePhaseFromPod(&instance, &existingPod)

		// Handle hibernation: if Idle and hibernation policy is set, check idle duration.
		if instance.Status.Phase == karov1alpha1.AgentInstancePhaseIdle &&
			instance.Spec.Hibernation.IdleAfter.Duration > 0 &&
			instance.Status.LastActiveAt != nil {
			idleDuration := time.Since(instance.Status.LastActiveAt.Time)
			if idleDuration > instance.Spec.Hibernation.IdleAfter.Duration {
				// Delete the pod and set phase to Hibernated.
				if err := r.Delete(ctx, &existingPod); err != nil && !errors.IsNotFound(err) {
					logger.Error(err, "failed to delete pod for hibernation")
				} else {
					instance.Status.Phase = karov1alpha1.AgentInstancePhaseHibernated
					instance.Status.PodRef = nil
					r.Recorder.Eventf(&instance, corev1.EventTypeNormal, "Hibernated",
						"Agent hibernated after %s idle", instance.Spec.Hibernation.IdleAfter.Duration)
				}
			}
		}
	}

	// Check context token exhaustion if running.
	if instance.Status.Phase == karov1alpha1.AgentInstancePhaseRunning &&
		agentSpec.Spec.MaxContextTokens > 0 &&
		instance.Status.ContextTokensUsed >= agentSpec.Spec.MaxContextTokens {
		switch agentSpec.Spec.OnContextExhaustion {
		case "terminate":
			instance.Status.Phase = karov1alpha1.AgentInstancePhaseTerminated
			r.Recorder.Event(&instance, corev1.EventTypeWarning, "ContextExhausted",
				"Context tokens exhausted, terminating agent")
		case "checkpoint":
			// Checkpoint: delete pod and hibernate.
			if podExists {
				if err := r.Delete(ctx, &existingPod); err != nil && !errors.IsNotFound(err) {
					logger.Error(err, "failed to delete pod for checkpoint")
				}
			}
			instance.Status.Phase = karov1alpha1.AgentInstancePhaseHibernated
			instance.Status.PodRef = nil
			r.Recorder.Event(&instance, corev1.EventTypeWarning, "ContextExhausted",
				"Context tokens exhausted, checkpointing agent")
		default: // "restart"
			if podExists {
				if err := r.Delete(ctx, &existingPod); err != nil && !errors.IsNotFound(err) {
					logger.Error(err, "failed to delete pod for restart")
				}
			}
			instance.Status.Phase = karov1alpha1.AgentInstancePhasePending
			instance.Status.PodRef = nil
			instance.Status.ContextTokensUsed = 0
			r.Recorder.Event(&instance, corev1.EventTypeWarning, "ContextExhausted",
				"Context tokens exhausted, restarting agent")
		}
	}

	// Set Ready condition
	readyStatus := metav1.ConditionFalse
	readyReason := "PodNotReady"
	readyMessage := "Waiting for pod to become ready"
	if instance.Status.Phase == karov1alpha1.AgentInstancePhaseRunning {
		readyStatus = metav1.ConditionTrue
		readyReason = "PodRunning"
		readyMessage = "Agent instance pod is running"
	}
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             readyStatus,
		ObservedGeneration: instance.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             readyReason,
		Message:            readyMessage,
	}
	setCondition(&instance.Status.Conditions, condition)

	if err := r.Status().Update(ctx, &instance); err != nil {
		logger.Error(err, "unable to update AgentInstance status")
		return ctrl.Result{}, err
	}

	logger.Info("reconciled AgentInstance", "phase", instance.Status.Phase, "pod", podName)
	return ctrl.Result{}, nil
}

// resolveEffectiveBindings resolves resource references from AgentSpec,
// falling back to AgentTeam shared resources if the agent is a team member.
func (r *AgentInstanceReconciler) resolveEffectiveBindings(ctx context.Context, agentSpec *karov1alpha1.AgentSpec) (*EffectiveBindings, error) {
	bindings := &EffectiveBindings{
		ModelConfigRef: agentSpec.Spec.ModelConfigRef.Name,
	}

	if agentSpec.Spec.MemoryRef != nil {
		bindings.MemoryRef = agentSpec.Spec.MemoryRef.Name
	}
	if agentSpec.Spec.ToolSetRef != nil {
		bindings.ToolSetRef = agentSpec.Spec.ToolSetRef.Name
	}
	if agentSpec.Spec.SandboxClassRef != nil {
		bindings.SandboxClassRef = agentSpec.Spec.SandboxClassRef.Name
	}

	// Look for an AgentTeam that includes this AgentSpec as a member
	team, err := r.findTeamForAgent(ctx, agentSpec)
	if err != nil {
		return bindings, err
	}
	if team == nil {
		return bindings, nil
	}

	// Apply team shared resources as fallback where AgentSpec has no override
	if bindings.ModelConfigRef == "" && team.Spec.SharedResources.ModelConfigRef != nil {
		bindings.ModelConfigRef = team.Spec.SharedResources.ModelConfigRef.Name
	}
	if bindings.MemoryRef == "" && team.Spec.SharedResources.MemoryRef != nil {
		bindings.MemoryRef = team.Spec.SharedResources.MemoryRef.Name
	}
	if bindings.ToolSetRef == "" && team.Spec.SharedResources.ToolSetRef != nil {
		bindings.ToolSetRef = team.Spec.SharedResources.ToolSetRef.Name
	}
	if bindings.SandboxClassRef == "" && team.Spec.SharedResources.SandboxClassRef != nil {
		bindings.SandboxClassRef = team.Spec.SharedResources.SandboxClassRef.Name
	}

	return bindings, nil
}

// findTeamForAgent searches for an AgentTeam that references the given AgentSpec.
func (r *AgentInstanceReconciler) findTeamForAgent(ctx context.Context, agentSpec *karov1alpha1.AgentSpec) (*karov1alpha1.AgentTeam, error) {
	var teamList karov1alpha1.AgentTeamList
	if err := r.List(ctx, &teamList, client.InNamespace(agentSpec.Namespace)); err != nil {
		return nil, err
	}
	for i := range teamList.Items {
		team := &teamList.Items[i]
		for _, member := range team.Spec.Agents {
			if member.AgentSpecRef.Name == agentSpec.Name {
				return team, nil
			}
		}
	}
	return nil, nil
}

// buildPod constructs a Pod specification for the AgentInstance.
func (r *AgentInstanceReconciler) buildPod(instance *karov1alpha1.AgentInstance, agentSpec *karov1alpha1.AgentSpec, podName string, bindings *EffectiveBindings) *corev1.Pod {
	labels := map[string]string{
		"karo.dev/agent-instance": instance.Name,
		"karo.dev/agent-spec":    agentSpec.Name,
	}

	// Build environment variables
	envVars := []corev1.EnvVar{
		{Name: "KARO_AGENT_INSTANCE", Value: instance.Name},
		{Name: "KARO_AGENT_SPEC", Value: agentSpec.Name},
		{Name: "KARO_NAMESPACE", Value: instance.Namespace},
	}
	if bindings != nil {
		if bindings.ModelConfigRef != "" {
			envVars = append(envVars, corev1.EnvVar{Name: "KARO_MODEL_CONFIG", Value: bindings.ModelConfigRef})
		}
		if bindings.MemoryRef != "" {
			envVars = append(envVars, corev1.EnvVar{Name: "KARO_MEMORY_STORE", Value: bindings.MemoryRef})
		}
		if bindings.ToolSetRef != "" {
			envVars = append(envVars, corev1.EnvVar{Name: "KARO_TOOL_SET", Value: bindings.ToolSetRef})
		}
		if bindings.SandboxClassRef != "" {
			envVars = append(envVars, corev1.EnvVar{Name: "KARO_SANDBOX_CLASS", Value: bindings.SandboxClassRef})
		}
	}

	// Main agent container
	mainContainer := corev1.Container{
		Name:      "agent",
		Image:     agentSpec.Spec.Runtime.Image,
		Resources: agentSpec.Spec.Runtime.Resources,
		Env:       envVars,
	}

	// MCP sidecar container
	mailboxName := fmt.Sprintf("%s-mailbox", agentSpec.Name)
	sidecar := corev1.Container{
		Name:  "agent-runtime-mcp",
		Image: "ghcr.io/karo-dev/agent-runtime-mcp:latest",
		Ports: []corev1.ContainerPort{
			{Name: "debug", ContainerPort: 9091, Protocol: corev1.ProtocolTCP},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		Env: []corev1.EnvVar{
			{Name: "KARO_AGENT_INSTANCE", Value: instance.Name},
			{Name: "KARO_AGENT_SPEC", Value: agentSpec.Name},
			{Name: "KARO_NAMESPACE", Value: instance.Namespace},
			{Name: "KARO_MAILBOX", Value: mailboxName},
			{Name: "KARO_MCP_TRANSPORT", Value: "stdio"},
		},
	}

	// Build init containers for git credentials if configured.
	var initContainers []corev1.Container
	var volumes []corev1.Volume
	if agentSpec.Spec.WorkspaceCredentials != nil && len(agentSpec.Spec.WorkspaceCredentials.Git) > 0 {
		gitInitContainer := gitinjector.BuildGitInitContainer(agentSpec, nil)
		initContainers = append(initContainers, gitInitContainer)
		volumes = append(volumes, corev1.Volume{
			Name: "home",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
		// Mount home volume in main container too so git config is available.
		mainContainer.VolumeMounts = append(mainContainer.VolumeMounts, corev1.VolumeMount{
			Name: "home", MountPath: "/root",
		})
	}

	// Mount agentConfigFiles from ConfigMaps.
	for _, acf := range agentSpec.Spec.AgentConfigFiles {
		if acf.Source.ConfigMapRef != nil {
			volName := fmt.Sprintf("agent-config-%s", acf.Name)
			volumes = append(volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: acf.Source.ConfigMapRef.Name,
						},
					},
				},
			})
			mainContainer.VolumeMounts = append(mainContainer.VolumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: acf.MountPath,
			})
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			InitContainers: initContainers,
			Containers:     []corev1.Container{mainContainer, sidecar},
			Volumes:        volumes,
			RestartPolicy:  corev1.RestartPolicyNever,
		},
	}

	return pod
}

// updatePhaseFromPod updates the AgentInstance phase based on the Pod status.
func (r *AgentInstanceReconciler) updatePhaseFromPod(instance *karov1alpha1.AgentInstance, pod *corev1.Pod) {
	switch pod.Status.Phase {
	case corev1.PodRunning:
		instance.Status.Phase = karov1alpha1.AgentInstancePhaseRunning
		now := metav1.Now()
		instance.Status.LastActiveAt = &now
	case corev1.PodPending:
		instance.Status.Phase = karov1alpha1.AgentInstancePhasePending
	case corev1.PodSucceeded, corev1.PodFailed:
		instance.Status.Phase = karov1alpha1.AgentInstancePhaseTerminated
	default:
		instance.Status.Phase = karov1alpha1.AgentInstancePhasePending
	}
}

func (r *AgentInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentInstance{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
