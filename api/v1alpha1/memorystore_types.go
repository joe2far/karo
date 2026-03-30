package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MemoryStoreSpec struct {
	Backend       MemoryBackend                 `json:"backend"`
	Scope         MemoryScope                   `json:"scope"`
	BoundAgents   []corev1.LocalObjectReference `json:"boundAgents,omitempty"`
	RetentionDays int32                         `json:"retentionDays,omitempty"`
	MaxMemories   int64                         `json:"maxMemories,omitempty"`
	Categories    []string                      `json:"categories,omitempty"`
}

type MemoryBackend struct {
	Type string      `json:"type"`
	Mem0 *Mem0Config `json:"mem0,omitempty"`
}

type Mem0Config struct {
	APIKeySecret   corev1.SecretKeySelector `json:"apiKeySecret"`
	OrganizationID string                   `json:"organizationId"`
	ProjectID      string                   `json:"projectId"`
}

type MemoryScope string

const (
	MemoryScopeAgentLocal MemoryScope = "agent-local"
	MemoryScopeTeam       MemoryScope = "team"
	MemoryScopeOrg        MemoryScope = "org"
)

type MemoryStoreStatus struct {
	Phase           string             `json:"phase,omitempty"`
	MemoryCount     int64              `json:"memoryCount,omitempty"`
	BackendEndpoint string             `json:"backendEndpoint,omitempty"`
	LastSyncedAt    *metav1.Time       `json:"lastSyncedAt,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Backend",type=string,JSONPath=`.spec.backend.type`
// +kubebuilder:printcolumn:name="Scope",type=string,JSONPath=`.spec.scope`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type MemoryStore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MemoryStoreSpec   `json:"spec,omitempty"`
	Status            MemoryStoreStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type MemoryStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MemoryStore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MemoryStore{}, &MemoryStoreList{})
}
