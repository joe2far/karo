// Package channel implements the webhook gateway pattern for AgentChannel.
//
// Architecture: Each AgentChannel CRD causes the controller to create:
//  1. A Deployment running the karo-channel-gateway image
//  2. A Service exposing the gateway on port 8443
//  3. The gateway handles inbound webhooks (Slack, Discord, Telegram, etc.)
//     and converts them to KARO TaskGraph/Mailbox operations
//  4. The gateway also posts outbound messages (task results, approval
//     requests) to the configured platform
//
// This follows the "sidecar-as-deployment" pattern used by OpenClaw and
// similar frameworks — one gateway per channel, managed by the operator.
package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

const (
	gatewayImage       = "ghcr.io/karo-dev/karo-channel-gateway:latest"
	gatewayPort        = 8443
	gatewayMetricsPort = 9090
)

// GatewayManager manages the lifecycle of channel gateway Deployments and Services.
type GatewayManager struct {
	client client.Client
}

// NewGatewayManager creates a new GatewayManager.
func NewGatewayManager(c client.Client) *GatewayManager {
	return &GatewayManager{client: c}
}

// EnsureGateway creates or updates the gateway Deployment and Service for an AgentChannel.
func (gm *GatewayManager) EnsureGateway(ctx context.Context, channel *karov1alpha1.AgentChannel) error {
	logger := log.FromContext(ctx)

	deployName := fmt.Sprintf("karo-gw-%s", channel.Name)
	svcName := fmt.Sprintf("karo-gw-%s", channel.Name)

	// Build the gateway config as a JSON env var.
	gwConfig, err := gm.buildGatewayConfig(channel)
	if err != nil {
		return fmt.Errorf("failed to build gateway config: %w", err)
	}

	// Ensure Deployment.
	if err := gm.ensureDeployment(ctx, channel, deployName, gwConfig); err != nil {
		logger.Error(err, "failed to ensure gateway deployment")
		return err
	}

	// Ensure Service.
	if err := gm.ensureService(ctx, channel, svcName, deployName); err != nil {
		logger.Error(err, "failed to ensure gateway service")
		return err
	}

	logger.Info("gateway ensured", "deployment", deployName, "service", svcName)
	return nil
}

// CleanupGateway removes the gateway Deployment and Service for an AgentChannel.
func (gm *GatewayManager) CleanupGateway(ctx context.Context, channel *karov1alpha1.AgentChannel) error {
	name := fmt.Sprintf("karo-gw-%s", channel.Name)

	// Delete Deployment.
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: channel.Namespace},
	}
	if err := gm.client.Delete(ctx, deploy); err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Delete Service.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: channel.Namespace},
	}
	if err := gm.client.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}

// GetGatewayEndpoint returns the in-cluster endpoint for the gateway Service.
func (gm *GatewayManager) GetGatewayEndpoint(channel *karov1alpha1.AgentChannel) string {
	svcName := fmt.Sprintf("karo-gw-%s", channel.Name)
	return fmt.Sprintf("http://%s.%s.svc:%d", svcName, channel.Namespace, gatewayPort)
}

func (gm *GatewayManager) ensureDeployment(ctx context.Context, channel *karov1alpha1.AgentChannel, name, gwConfig string) error {
	replicas := int32(1)
	labels := map[string]string{
		"karo.dev/component":     "channel-gateway",
		"karo.dev/agent-channel": channel.Name,
	}

	// Build env vars from the channel's platform secrets.
	envVars := []corev1.EnvVar{
		{Name: "KARO_CHANNEL_NAME", Value: channel.Name},
		{Name: "KARO_NAMESPACE", Value: channel.Namespace},
		{Name: "KARO_PLATFORM", Value: string(channel.Spec.Platform.Type)},
		{Name: "KARO_GATEWAY_CONFIG", Value: gwConfig},
		{Name: "KARO_GATEWAY_PORT", Value: fmt.Sprintf("%d", gatewayPort)},
	}
	envVars = append(envVars, gm.platformEnvVars(channel)...)

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: channel.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "karo.dev/v1alpha1",
					Kind:               "AgentChannel",
					Name:               channel.Name,
					UID:                channel.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
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
							Image: gatewayImage,
							Ports: []corev1.ContainerPort{
								{Name: "webhook", ContainerPort: int32(gatewayPort), Protocol: corev1.ProtocolTCP},
								{Name: "metrics", ContainerPort: int32(gatewayMetricsPort), Protocol: corev1.ProtocolTCP},
							},
							Env: envVars,
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
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(int32(gatewayPort)),
									},
								},
								PeriodSeconds: 30,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt32(int32(gatewayPort)),
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
	key := types.NamespacedName{Name: name, Namespace: channel.Namespace}
	if err := gm.client.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return gm.client.Create(ctx, desired)
		}
		return err
	}

	// Update the deployment spec.
	existing.Spec.Template = desired.Spec.Template
	return gm.client.Update(ctx, &existing)
}

func (gm *GatewayManager) ensureService(ctx context.Context, channel *karov1alpha1.AgentChannel, svcName, deployName string) error {
	labels := map[string]string{
		"karo.dev/component":     "channel-gateway",
		"karo.dev/agent-channel": channel.Name,
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: channel.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "karo.dev/v1alpha1",
					Kind:               "AgentChannel",
					Name:               channel.Name,
					UID:                channel.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "webhook",
					Port:       int32(gatewayPort),
					TargetPort: intstr.FromString("webhook"),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "metrics",
					Port:       int32(gatewayMetricsPort),
					TargetPort: intstr.FromString("metrics"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	var existing corev1.Service
	key := types.NamespacedName{Name: svcName, Namespace: channel.Namespace}
	if err := gm.client.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return gm.client.Create(ctx, desired)
		}
		return err
	}

	existing.Spec.Ports = desired.Spec.Ports
	existing.Spec.Selector = desired.Spec.Selector
	return gm.client.Update(ctx, &existing)
}

// platformEnvVars generates platform-specific env vars that inject secrets.
func (gm *GatewayManager) platformEnvVars(channel *karov1alpha1.AgentChannel) []corev1.EnvVar {
	var envs []corev1.EnvVar
	p := channel.Spec.Platform

	switch p.Type {
	case karov1alpha1.ChannelPlatformSlack:
		if p.Slack != nil {
			envs = append(envs,
				envFromSecret("SLACK_BOT_TOKEN", p.Slack.AppCredentialSecret),
				envFromSecret("SLACK_SIGNING_SECRET", p.Slack.SigningSecret),
			)
			if p.Slack.ChannelID != "" {
				envs = append(envs, corev1.EnvVar{Name: "SLACK_CHANNEL_ID", Value: p.Slack.ChannelID})
			}
			// Build the effective channel allowlist: the primary channelId
			// plus any additional channelIds. The gateway uses this to drop
			// inbound events that originate outside the allowlist.
			allowedChannels := slackAllowedChannels(p.Slack)
			if len(allowedChannels) > 0 {
				envs = append(envs, corev1.EnvVar{
					Name:  "SLACK_ALLOWED_CHANNEL_IDS",
					Value: strings.Join(allowedChannels, ","),
				})
			}
			if len(p.Slack.AllowedUserIDs) > 0 {
				envs = append(envs, corev1.EnvVar{
					Name:  "SLACK_ALLOWED_USER_IDS",
					Value: strings.Join(p.Slack.AllowedUserIDs, ","),
				})
			}
			if p.Slack.RequireMention {
				envs = append(envs, corev1.EnvVar{Name: "SLACK_REQUIRE_MENTION", Value: "true"})
			}
			if p.Slack.AllowDirectMessages {
				envs = append(envs, corev1.EnvVar{Name: "SLACK_ALLOW_DM", Value: "true"})
			}
			// IgnoreBots defaults to true when unset.
			ignoreBots := true
			if p.Slack.IgnoreBots != nil {
				ignoreBots = *p.Slack.IgnoreBots
			}
			envs = append(envs, corev1.EnvVar{
				Name:  "SLACK_IGNORE_BOTS",
				Value: fmt.Sprintf("%t", ignoreBots),
			})
			if p.Slack.SocketMode && p.Slack.AppToken != nil {
				envs = append(envs, envFromSecret("SLACK_APP_TOKEN", *p.Slack.AppToken))
			}
		}
	case karov1alpha1.ChannelPlatformTelegram:
		if p.Telegram != nil {
			envs = append(envs, envFromSecret("TELEGRAM_BOT_TOKEN", p.Telegram.BotTokenSecret))
			if p.Telegram.ChatID != "" {
				envs = append(envs, corev1.EnvVar{Name: "TELEGRAM_CHAT_ID", Value: p.Telegram.ChatID})
			}
		}
	case karov1alpha1.ChannelPlatformDiscord:
		if p.Discord != nil {
			envs = append(envs,
				envFromSecret("DISCORD_BOT_TOKEN", p.Discord.BotTokenSecret),
			)
			envs = append(envs,
				corev1.EnvVar{Name: "DISCORD_GUILD_ID", Value: p.Discord.GuildID},
				corev1.EnvVar{Name: "DISCORD_CHANNEL_ID", Value: p.Discord.ChannelID},
			)
		}
	case karov1alpha1.ChannelPlatformTeams:
		if p.Teams != nil {
			envs = append(envs,
				envFromSecret("TEAMS_APP_CREDENTIAL", p.Teams.AppCredentialSecret),
			)
			envs = append(envs, corev1.EnvVar{Name: "TEAMS_TENANT_ID", Value: p.Teams.TenantID})
		}
	case karov1alpha1.ChannelPlatformWebhook:
		if p.Webhook != nil {
			envs = append(envs,
				corev1.EnvVar{Name: "WEBHOOK_INBOUND_URL", Value: p.Webhook.InboundURL},
				corev1.EnvVar{Name: "WEBHOOK_OUTBOUND_URL", Value: p.Webhook.OutboundURL},
			)
			if p.Webhook.AuthSecret != nil {
				envs = append(envs, envFromSecret("WEBHOOK_AUTH_TOKEN", *p.Webhook.AuthSecret))
			}
		}
	}

	return envs
}

// buildGatewayConfig serializes the channel spec into a JSON config for the gateway.
func (gm *GatewayManager) buildGatewayConfig(channel *karov1alpha1.AgentChannel) (string, error) {
	config := map[string]interface{}{
		"platform":    channel.Spec.Platform.Type,
		"inbound":     channel.Spec.Inbound,
		"outbound":    channel.Spec.Outbound,
		"approvals":   channel.Spec.Approvals,
		"teamHandoff": channel.Spec.TeamHandoff,
	}
	b, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func envFromSecret(envName string, sel corev1.SecretKeySelector) corev1.EnvVar {
	return corev1.EnvVar{
		Name: envName,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: sel.Name},
				Key:                  sel.Key,
			},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

// slackAllowedChannels returns the deduplicated allowlist of Slack channel IDs
// the bot may interact with: the primary channelId plus any additional
// channelIds. Order is stable: primary first, additions in spec order.
func slackAllowedChannels(s *karov1alpha1.SlackConfig) []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(s.ChannelIDs)+1)
	out := make([]string, 0, len(s.ChannelIDs)+1)
	if s.ChannelID != "" {
		seen[s.ChannelID] = struct{}{}
		out = append(out, s.ChannelID)
	}
	for _, id := range s.ChannelIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
