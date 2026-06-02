// Package v1 contains the karo.dev/v1 API types for the KARO operator.
//
// The AgentTeam spec body is the shared, compiled contract from
// PRD-KARO-CLI.md §4.2 — identical to what the CLI emits via `karo export`.
// The operator adds only the runtime: block and status: (PRD-KARO-v2.md §3.1).
// +kubebuilder:object:generate=true
// +groupName=karo.dev
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group/version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "karo.dev", Version: "v1"}

	// SchemeBuilder registers the types with a Scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types to a Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// AcceptedAPIVersions mirrors karo-runtime's accepted set (CLI §4.4). Any other
// apiVersion is rejected with an upgrade message rather than silently coerced.
var AcceptedAPIVersions = []string{"karo.dev/v1"}
