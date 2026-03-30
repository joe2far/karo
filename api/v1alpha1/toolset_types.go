package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ToolSetSpec struct {
	// +kubebuilder:validation:MinItems=1
	Tools     []ToolEntry                  `json:"tools"`
	PolicyRef *corev1.LocalObjectReference `json:"policyRef,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.transport != 'stdio' || size(self.command) > 0",message="command required for stdio transport"
// +kubebuilder:validation:XValidation:rule="self.transport == 'stdio' || self.endpoint != ''",message="endpoint required for sse and streamable-http transport"
type ToolEntry struct {
	// +kubebuilder:validation:MinLength=1
	Name             string                    `json:"name"`
	// +kubebuilder:validation:Enum=mcp
	Type             string                    `json:"type"`
	// +kubebuilder:validation:Enum=stdio;sse;streamable-http
	Transport        MCPTransport              `json:"transport"`
	Endpoint         string                    `json:"endpoint,omitempty"`
	Command          []string                  `json:"command,omitempty"`
	Permissions      []string                  `json:"permissions,omitempty"`
	CredentialSecret *corev1.SecretKeySelector `json:"credentialSecret,omitempty"`
	SandboxRequired  bool                      `json:"sandboxRequired,omitempty"`
	Builtin          bool                      `json:"builtin,omitempty"`
}

type MCPTransport string

const (
	MCPTransportStdio          MCPTransport = "stdio"
	MCPTransportSSE            MCPTransport = "sse"
	MCPTransportStreamableHTTP MCPTransport = "streamable-http"
)

type ToolSetStatus struct {
	Phase          string             `json:"phase,omitempty"`
	AvailableTools int32              `json:"availableTools,omitempty"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tools",type=integer,JSONPath=`.status.availableTools`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type ToolSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ToolSetSpec   `json:"spec,omitempty"`
	Status            ToolSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ToolSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ToolSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ToolSet{}, &ToolSetList{})
}
