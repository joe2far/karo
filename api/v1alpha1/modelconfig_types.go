package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ModelConfigSpec struct {
	Provider     string                    `json:"provider"`
	Name         string                    `json:"name"`
	APIKeySecret *corev1.SecretKeySelector `json:"apiKeySecret,omitempty"`
	Endpoint     string                    `json:"endpoint,omitempty"`
	Bedrock      *BedrockConfig            `json:"bedrock,omitempty"`
	Vertex       *VertexConfig             `json:"vertex,omitempty"`
	Parameters   ModelParameters           `json:"parameters,omitempty"`
	RateLimit    ModelRateLimit            `json:"rateLimit,omitempty"`
}

type BedrockConfig struct {
	Region           string `json:"region"`
	IRSARoleArn      string `json:"irsaRoleArn"`
	EndpointOverride string `json:"endpointOverride,omitempty"`
}

type VertexConfig struct {
	Project           string `json:"project"`
	Location          string `json:"location"`
	GCPServiceAccount string `json:"gcpServiceAccount"`
	EndpointOverride  string `json:"endpointOverride,omitempty"`
}

type ModelParameters struct {
	MaxTokens   int32   `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
}

type ModelRateLimit struct {
	RequestsPerMinute int32 `json:"requestsPerMinute,omitempty"`
	TokensPerMinute   int64 `json:"tokensPerMinute,omitempty"`
	TokensPerDay      int64 `json:"tokensPerDay,omitempty"`
}

type ModelConfigStatus struct {
	Phase            string             `json:"phase,omitempty"`
	Provider         string             `json:"provider,omitempty"`
	ResolvedEndpoint string             `json:"resolvedEndpoint,omitempty"`
	LastValidatedAt  *metav1.Time       `json:"lastValidatedAt,omitempty"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type ModelConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ModelConfigSpec   `json:"spec,omitempty"`
	Status            ModelConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ModelConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelConfig{}, &ModelConfigList{})
}
