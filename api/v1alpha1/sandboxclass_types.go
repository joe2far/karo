package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule="!(self.securityContext.runAsNonRoot && has(self.securityContext.runAsUser) && self.securityContext.runAsUser == 0)",message="runAsUser cannot be 0 when runAsNonRoot is true"
type SandboxClassSpec struct {
	// +kubebuilder:validation:MinLength=1
	RuntimeClassName string                `json:"runtimeClassName"`
	NetworkPolicy    SandboxNetworkPolicy  `json:"networkPolicy,omitempty"`
	Filesystem       FilesystemConfig      `json:"filesystem,omitempty"`
	ResourceLimits   corev1.ResourceList   `json:"resourceLimits,omitempty"`
	SecurityContext  SecurityContextConfig `json:"securityContext,omitempty"`
}

type SandboxNetworkPolicy struct {
	// +kubebuilder:validation:Enum=restricted;open;none
	Egress         string   `json:"egress"`
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	AllowedCIDRs   []string `json:"allowedCIDRs,omitempty"`
}

type FilesystemConfig struct {
	ReadOnlyRootFilesystem bool     `json:"readOnlyRootFilesystem,omitempty"`
	AllowedMounts          []string `json:"allowedMounts,omitempty"`
}

type SecurityContextConfig struct {
	RunAsNonRoot             bool                   `json:"runAsNonRoot,omitempty"`
	// +kubebuilder:validation:Minimum=0
	RunAsUser                *int64                 `json:"runAsUser,omitempty"`
	AllowPrivilegeEscalation bool                   `json:"allowPrivilegeEscalation,omitempty"`
	SeccompProfile           *corev1.SeccompProfile `json:"seccompProfile,omitempty"`
	Capabilities             *corev1.Capabilities   `json:"capabilities,omitempty"`
}

type SandboxClassStatus struct {
	Phase                 string             `json:"phase,omitempty"`
	RuntimeClassAvailable bool               `json:"runtimeClassAvailable,omitempty"`
	Conditions            []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="RuntimeClass",type=string,JSONPath=`.spec.runtimeClassName`
type SandboxClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SandboxClassSpec   `json:"spec,omitempty"`
	Status            SandboxClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type SandboxClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SandboxClass{}, &SandboxClassList{})
}
