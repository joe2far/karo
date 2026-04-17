package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const (
	agentGatewayFinalizer = "karo.dev/agentgateway-finalizer"
	defaultGatewayImage   = "ghcr.io/agentgateway/agentgateway:latest"
)

// +kubebuilder:rbac:groups=karo.dev,resources=agentgateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.dev,resources=agentgateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.dev,resources=agentgateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=karo.dev,resources=modelconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=karo.dev,resources=toolsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// AgentGatewayReconciler reconciles an AgentGateway object. It materializes
// a Deployment + Service running the agent gateway proxy, resolves the
// upstream ModelConfig / ToolSet references into concrete endpoints, and
// reports readiness via status.
type AgentGatewayReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *AgentGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var gw karov1alpha1.AgentGateway
	if err := r.Get(ctx, req.NamespacedName, &gw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !gw.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&gw, agentGatewayFinalizer) {
			controllerutil.RemoveFinalizer(&gw, agentGatewayFinalizer)
			if err := r.Update(ctx, &gw); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&gw, agentGatewayFinalizer) {
		controllerutil.AddFinalizer(&gw, agentGatewayFinalizer)
		if err := r.Update(ctx, &gw); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve upstream references. Missing ModelConfig or ToolSet puts the
	// gateway into Degraded — the Deployment is still rolled out so that
	// healthy upstreams continue to serve traffic.
	resolved, unresolved := r.resolveUpstreams(ctx, &gw)

	if err := r.ensureDeployment(ctx, &gw); err != nil {
		logger.Error(err, "failed to ensure agent gateway deployment")
		return ctrl.Result{}, err
	}
	if err := r.ensureService(ctx, &gw); err != nil {
		logger.Error(err, "failed to ensure agent gateway service")
		return ctrl.Result{}, err
	}

	readyReplicas := r.readyReplicas(ctx, &gw)
	gw.Status.ReadyReplicas = readyReplicas
	gw.Status.ResolvedUpstreams = resolved
	gw.Status.ResolvedEndpoint = r.gatewayEndpoint(&gw)
	now := metav1.Now()
	gw.Status.LastReconciledAt = &now

	switch {
	case readyReplicas == 0:
		gw.Status.Phase = "Pending"
	case len(unresolved) > 0:
		gw.Status.Phase = "Degraded"
	default:
		gw.Status.Phase = "Ready"
	}

	cond := metav1.Condition{
		Type:               "UpstreamsResolved",
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if len(unresolved) == 0 {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "AllUpstreamsResolved"
		cond.Message = fmt.Sprintf("%d upstream(s) resolved", resolved)
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "UpstreamsMissing"
		cond.Message = fmt.Sprintf("unresolved: %s", strings.Join(unresolved, ","))
		r.Recorder.Event(&gw, corev1.EventTypeWarning, "UpstreamsMissing", cond.Message)
	}
	setCondition(&gw.Status.Conditions, cond)

	readyCond := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if gw.Status.Phase == "Ready" {
		readyCond.Status = metav1.ConditionTrue
		readyCond.Reason = "GatewayRunning"
		readyCond.Message = fmt.Sprintf("gateway serving at %s", gw.Status.ResolvedEndpoint)
	} else {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = "GatewayNotReady"
		readyCond.Message = fmt.Sprintf("phase=%s readyReplicas=%d", gw.Status.Phase, readyReplicas)
	}
	setCondition(&gw.Status.Conditions, readyCond)

	if err := r.Status().Update(ctx, &gw); err != nil {
		logger.Error(err, "unable to update AgentGateway status")
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(&gw, corev1.EventTypeNormal, "Reconciled",
		"AgentGateway reconciled (phase=%s, upstreams=%d)", gw.Status.Phase, resolved)
	return ctrl.Result{}, nil
}

// resolveUpstreams checks that each referenced ModelConfig / ToolSet exists
// in the gateway's namespace. Returns the count of resolved upstreams and
// the names of any that could not be resolved.
func (r *AgentGatewayReconciler) resolveUpstreams(ctx context.Context, gw *karov1alpha1.AgentGateway) (int32, []string) {
	var resolved int32
	var missing []string

	for _, m := range gw.Spec.Upstreams.Models {
		var mc karov1alpha1.ModelConfig
		key := types.NamespacedName{Name: m.ModelConfigRef.Name, Namespace: gw.Namespace}
		if err := r.Get(ctx, key, &mc); err != nil {
			missing = append(missing, fmt.Sprintf("model/%s", m.ModelConfigRef.Name))
			continue
		}
		resolved++
	}
	for _, t := range gw.Spec.Upstreams.Tools {
		var ts karov1alpha1.ToolSet
		key := types.NamespacedName{Name: t.ToolSetRef.Name, Namespace: gw.Namespace}
		if err := r.Get(ctx, key, &ts); err != nil {
			missing = append(missing, fmt.Sprintf("tool/%s", t.ToolSetRef.Name))
			continue
		}
		resolved++
	}
	// A2A agent upstreams are opaque endpoints — they are counted as
	// resolved regardless, with reachability checks deferred to the gateway
	// process itself.
	resolved += int32(len(gw.Spec.Upstreams.Agents))
	return resolved, missing
}

func (r *AgentGatewayReconciler) ensureDeployment(ctx context.Context, gw *karov1alpha1.AgentGateway) error {
	name := gatewayDeploymentName(gw)
	image := gw.Spec.Image
	if image == "" {
		image = defaultGatewayImage
	}
	listenPort := gw.Spec.ListenPort
	if listenPort == 0 {
		listenPort = 8080
	}
	metricsPort := gw.Spec.Observability.MetricsPort
	if metricsPort == 0 {
		metricsPort = 9090
	}
	replicas := gw.Spec.Replicas
	if replicas == 0 {
		replicas = 1
	}

	labels := map[string]string{
		"karo.dev/component":     "agent-gateway",
		"karo.dev/agent-gateway": gw.Name,
	}

	resources := gw.Spec.Resources
	if len(resources.Requests) == 0 {
		resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		}
	}
	if len(resources.Limits) == 0 {
		resources.Limits = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		}
	}

	envVars := []corev1.EnvVar{
		{Name: "KARO_GATEWAY_NAME", Value: gw.Name},
		{Name: "KARO_GATEWAY_NAMESPACE", Value: gw.Namespace},
		{Name: "KARO_GATEWAY_PORT", Value: fmt.Sprintf("%d", listenPort)},
		{Name: "KARO_GATEWAY_METRICS_PORT", Value: fmt.Sprintf("%d", metricsPort)},
		{Name: "KARO_GATEWAY_UPSTREAM_MODELS", Value: joinUpstreamModels(gw.Spec.Upstreams.Models)},
		{Name: "KARO_GATEWAY_UPSTREAM_TOOLS", Value: joinUpstreamTools(gw.Spec.Upstreams.Tools)},
	}
	if gw.Spec.Policy.Auth != nil && gw.Spec.Policy.Auth.BearerSecret != nil {
		envVars = append(envVars, corev1.EnvVar{
			Name: "KARO_GATEWAY_BEARER_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: gw.Spec.Policy.Auth.BearerSecret.Name},
					Key:                  gw.Spec.Policy.Auth.BearerSecret.Key,
				},
			},
		})
	}

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       gw.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{gatewayOwnerRef(gw)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "gateway",
							Image: image,
							Ports: []corev1.ContainerPort{
								{Name: "proxy", ContainerPort: listenPort, Protocol: corev1.ProtocolTCP},
								{Name: "metrics", ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP},
							},
							Env:       envVars,
							Resources: resources,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(listenPort),
									},
								},
								PeriodSeconds: 30,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt32(listenPort),
									},
								},
								PeriodSeconds: 10,
							},
						},
					},
				},
			},
		},
	}

	var existing appsv1.Deployment
	key := types.NamespacedName{Name: name, Namespace: gw.Namespace}
	if err := r.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}
	existing.Spec.Replicas = desired.Spec.Replicas
	existing.Spec.Template = desired.Spec.Template
	return r.Update(ctx, &existing)
}

func (r *AgentGatewayReconciler) ensureService(ctx context.Context, gw *karov1alpha1.AgentGateway) error {
	name := gatewayServiceName(gw)
	listenPort := gw.Spec.ListenPort
	if listenPort == 0 {
		listenPort = 8080
	}
	metricsPort := gw.Spec.Observability.MetricsPort
	if metricsPort == 0 {
		metricsPort = 9090
	}
	labels := map[string]string{
		"karo.dev/component":     "agent-gateway",
		"karo.dev/agent-gateway": gw.Name,
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       gw.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{gatewayOwnerRef(gw)},
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "proxy", Port: listenPort, TargetPort: intstr.FromString("proxy"), Protocol: corev1.ProtocolTCP},
				{Name: "metrics", Port: metricsPort, TargetPort: intstr.FromString("metrics"), Protocol: corev1.ProtocolTCP},
			},
		},
	}

	var existing corev1.Service
	key := types.NamespacedName{Name: name, Namespace: gw.Namespace}
	if err := r.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}
	existing.Spec.Ports = desired.Spec.Ports
	existing.Spec.Selector = desired.Spec.Selector
	return r.Update(ctx, &existing)
}

func (r *AgentGatewayReconciler) readyReplicas(ctx context.Context, gw *karov1alpha1.AgentGateway) int32 {
	var deploy appsv1.Deployment
	key := types.NamespacedName{Name: gatewayDeploymentName(gw), Namespace: gw.Namespace}
	if err := r.Get(ctx, key, &deploy); err != nil {
		return 0
	}
	return deploy.Status.AvailableReplicas
}

func (r *AgentGatewayReconciler) gatewayEndpoint(gw *karov1alpha1.AgentGateway) string {
	port := gw.Spec.ListenPort
	if port == 0 {
		port = 8080
	}
	return fmt.Sprintf("http://%s.%s.svc:%d", gatewayServiceName(gw), gw.Namespace, port)
}

func (r *AgentGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.AgentGateway{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

func gatewayDeploymentName(gw *karov1alpha1.AgentGateway) string {
	return fmt.Sprintf("karo-agw-%s", gw.Name)
}

func gatewayServiceName(gw *karov1alpha1.AgentGateway) string {
	return fmt.Sprintf("karo-agw-%s", gw.Name)
}

func gatewayOwnerRef(gw *karov1alpha1.AgentGateway) metav1.OwnerReference {
	controller := true
	blockOwner := true
	return metav1.OwnerReference{
		APIVersion:         "karo.dev/v1alpha1",
		Kind:               "AgentGateway",
		Name:               gw.Name,
		UID:                gw.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwner,
	}
}

func joinUpstreamModels(models []karov1alpha1.GatewayModelUpstream) string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name+"="+m.ModelConfigRef.Name)
	}
	return strings.Join(names, ",")
}

func joinUpstreamTools(tools []karov1alpha1.GatewayToolUpstream) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name+"="+t.ToolSetRef.Name)
	}
	return strings.Join(names, ",")
}
