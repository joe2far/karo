package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule="self.platform.type != 'slack' || has(self.platform.slack)",message="slack config required when platform type is slack"
// +kubebuilder:validation:XValidation:rule="self.platform.type != 'telegram' || has(self.platform.telegram)",message="telegram config required when platform type is telegram"
// +kubebuilder:validation:XValidation:rule="self.platform.type != 'discord' || has(self.platform.discord)",message="discord config required when platform type is discord"
// +kubebuilder:validation:XValidation:rule="self.platform.type != 'teams' || has(self.platform.teams)",message="teams config required when platform type is teams"
// +kubebuilder:validation:XValidation:rule="self.platform.type != 'webhook' || has(self.platform.webhook)",message="webhook config required when platform type is webhook"
type AgentChannelSpec struct {
	Platform    ChannelPlatform   `json:"platform"`
	Inbound     InboundConfig     `json:"inbound"`
	Outbound    OutboundConfig    `json:"outbound"`
	Approvals   ApprovalConfig    `json:"approvals,omitempty"`
	TeamHandoff []TeamHandoffRule `json:"teamHandoff,omitempty"`
}

type ChannelPlatform struct {
	// +kubebuilder:validation:Enum=slack;telegram;discord;teams;webhook
	Type     ChannelPlatformType `json:"type"`
	Slack    *SlackConfig        `json:"slack,omitempty"`
	Telegram *TelegramConfig     `json:"telegram,omitempty"`
	Discord  *DiscordConfig      `json:"discord,omitempty"`
	Teams    *TeamsConfig        `json:"teams,omitempty"`
	Webhook  *WebhookConfig      `json:"webhook,omitempty"`
}

type ChannelPlatformType string

const (
	ChannelPlatformSlack    ChannelPlatformType = "slack"
	ChannelPlatformTelegram ChannelPlatformType = "telegram"
	ChannelPlatformDiscord  ChannelPlatformType = "discord"
	ChannelPlatformTeams    ChannelPlatformType = "teams"
	ChannelPlatformWebhook  ChannelPlatformType = "webhook"
)

type SlackConfig struct {
	AppCredentialSecret corev1.SecretKeySelector  `json:"appCredentialSecret"`
	SigningSecret       corev1.SecretKeySelector  `json:"signingSecret"`
	AppToken            *corev1.SecretKeySelector `json:"appToken,omitempty"`
	// +kubebuilder:validation:MinLength=1
	ChannelID           string                    `json:"channelId"`
	SocketMode          bool                      `json:"socketMode,omitempty"`
	AllowedUserIDs      []string                  `json:"allowedUserIds,omitempty"`
	ThreadReplies       bool                      `json:"threadReplies,omitempty"`
}

type TelegramConfig struct {
	BotTokenSecret corev1.SecretKeySelector `json:"botTokenSecret"`
	ChatID         string                   `json:"chatId,omitempty"`
	AllowedUserIDs []string                 `json:"allowedUserIds,omitempty"`
	// +kubebuilder:validation:Enum=allow;deny
	DMPolicy       string                   `json:"dmPolicy,omitempty"`
	InlineKeyboard bool                     `json:"inlineKeyboard,omitempty"`
}

type DiscordConfig struct {
	BotTokenSecret       corev1.SecretKeySelector `json:"botTokenSecret"`
	// +kubebuilder:validation:MinLength=1
	GuildID              string                   `json:"guildId"`
	// +kubebuilder:validation:MinLength=1
	ChannelID            string                   `json:"channelId"`
	AllowedRoleIDs       []string                 `json:"allowedRoleIds,omitempty"`
	ThreadReplies        bool                     `json:"threadReplies,omitempty"`
	MessageContentIntent bool                     `json:"messageContentIntent,omitempty"`
}

type TeamsConfig struct {
	AppCredentialSecret corev1.SecretKeySelector `json:"appCredentialSecret"`
	// +kubebuilder:validation:MinLength=1
	TenantID            string                   `json:"tenantId"`
	TeamID              string                   `json:"teamId,omitempty"`
	ChannelID           string                   `json:"channelId,omitempty"`
	AllowedUserIDs      []string                 `json:"allowedUserIds,omitempty"`
}

type WebhookConfig struct {
	// +kubebuilder:validation:MinLength=1
	InboundURL  string                    `json:"inboundUrl"`
	// +kubebuilder:validation:MinLength=1
	OutboundURL string                    `json:"outboundUrl"`
	AuthSecret  *corev1.SecretKeySelector `json:"authSecret,omitempty"`
}

type InboundConfig struct {
	DefaultTeamRef    corev1.LocalObjectReference `json:"defaultTeamRef"`
	// +kubebuilder:validation:Enum=task-creation;human-override;auto
	Mode              InboundMode                 `json:"mode"`
	TaskGraphTemplate *TaskGraphTemplate          `json:"taskGraphTemplate,omitempty"`
	AutoRoute         *AutoRouteConfig            `json:"autoRoute,omitempty"`
}

type InboundMode string

const (
	InboundModeTaskCreation  InboundMode = "task-creation"
	InboundModeHumanOverride InboundMode = "human-override"
	InboundModeAuto          InboundMode = "auto"
)

type TaskGraphTemplate struct {
	OwnerAgentRef   corev1.LocalObjectReference `json:"ownerAgentRef"`
	DispatcherRef   corev1.LocalObjectReference `json:"dispatcherRef"`
	// +kubebuilder:validation:Enum=design;impl;eval;review;infra;approval
	InitialTaskType TaskType                    `json:"initialTaskType"`
}

type AutoRouteConfig struct {
	ModelConfigRef       corev1.LocalObjectReference `json:"modelConfigRef"`
	ClassificationPrompt SystemPromptConfig          `json:"classificationPrompt"`
}

type OutboundConfig struct {
	NotifyOn              []ChannelEvent               `json:"notifyOn"`
	// +kubebuilder:validation:Enum=summary;detailed;minimal
	Format                OutboundFormat               `json:"format"`
	SummaryModelConfigRef *corev1.LocalObjectReference `json:"summaryModelConfigRef,omitempty"`
}

type ChannelEvent string

const (
	ChannelEventTaskGraphCreated   ChannelEvent = "taskGraphCreated"
	ChannelEventTaskGraphCompleted ChannelEvent = "taskGraphCompleted"
	ChannelEventTaskFailed         ChannelEvent = "taskFailed"
	ChannelEventApprovalRequired   ChannelEvent = "approvalRequired"
	ChannelEventEvalGateFailed     ChannelEvent = "evalGateFailed"
)

type OutboundFormat string

const (
	OutboundFormatSummary  OutboundFormat = "summary"
	OutboundFormatDetailed OutboundFormat = "detailed"
	OutboundFormatMinimal  OutboundFormat = "minimal"
)

type ApprovalConfig struct {
	Enabled        bool          `json:"enabled"`
	// +kubebuilder:validation:Enum=interactive;reply
	Style          ApprovalStyle `json:"style"`
	// +kubebuilder:validation:Minimum=1
	TimeoutMinutes int32         `json:"timeoutMinutes,omitempty"`
	// +kubebuilder:validation:Enum=Fail;Escalate;Approve
	OnTimeout      string        `json:"onTimeout"`
}

type ApprovalStyle string

const (
	ApprovalStyleInteractive ApprovalStyle = "interactive"
	ApprovalStyleReply       ApprovalStyle = "reply"
)

type TeamHandoffRule struct {
	FromTeamRef              corev1.LocalObjectReference `json:"fromTeamRef"`
	ToTeamRef                corev1.LocalObjectReference `json:"toTeamRef"`
	// +kubebuilder:validation:MinLength=1
	Trigger                  string                      `json:"trigger"`
	RequireApproval          bool                        `json:"requireApproval"`
	HandoffTaskGraphTemplate *TaskGraphTemplate          `json:"handoffTaskGraphTemplate,omitempty"`
	InjectUpstreamContext    bool                        `json:"injectUpstreamContext,omitempty"`
}

type AgentChannelActiveTaskGraph struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Team  string `json:"team"`
}

type AgentChannelStatus struct {
	Phase                 string                        `json:"phase,omitempty"`
	PlatformConnected     bool                          `json:"platformConnected,omitempty"`
	LastInboundMessageAt  *metav1.Time                  `json:"lastInboundMessageAt,omitempty"`
	LastOutboundMessageAt *metav1.Time                  `json:"lastOutboundMessageAt,omitempty"`
	TotalInboundMessages  int64                         `json:"totalInboundMessages,omitempty"`
	TotalOutboundMessages int64                         `json:"totalOutboundMessages,omitempty"`
	PendingApprovals      int32                         `json:"pendingApprovals,omitempty"`
	ActiveTaskGraphs      []AgentChannelActiveTaskGraph `json:"activeTaskGraphs,omitempty"`
	Conditions            []metav1.Condition            `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Platform",type=string,JSONPath=`.spec.platform.type`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type AgentChannel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentChannelSpec   `json:"spec,omitempty"`
	Status            AgentChannelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentChannel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentChannel{}, &AgentChannelList{})
}
