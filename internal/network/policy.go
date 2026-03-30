package network

import (
	"context"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
)

// CNIType represents the detected CNI/network plugin.
type CNIType string

const (
	CNICilium   CNIType = "cilium"
	CNIStandard CNIType = "standard"
)

// PolicyManager creates and manages network policies for SandboxClass resources.
type PolicyManager struct {
	client client.Client
}

// NewPolicyManager creates a new PolicyManager.
func NewPolicyManager(c client.Client) *PolicyManager {
	return &PolicyManager{client: c}
}

// DetectCNI checks whether Cilium is available in the cluster by probing for
// the CiliumNetworkPolicy CRD. On GKE Dataplane V2, Cilium is the default.
func (pm *PolicyManager) DetectCNI(ctx context.Context) CNIType {
	// Check if CiliumNetworkPolicy CRD exists by trying to list it.
	ciliumGVR := schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumnetworkpolicies",
	}

	probe := &unstructured.UnstructuredList{}
	probe.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ciliumGVR.Group,
		Version: ciliumGVR.Version,
		Kind:    "CiliumNetworkPolicyList",
	})

	// Try listing in kube-system — if the CRD exists, the list call succeeds
	// (even if empty). If it doesn't, we get a "no matches" error.
	if err := pm.client.List(ctx, probe, &client.ListOptions{
		Namespace: "kube-system",
		Limit:     1,
	}); err == nil {
		return CNICilium
	}

	return CNIStandard
}

// EnsureNetworkPolicy creates or updates the appropriate network policy for a
// SandboxClass. Uses CiliumNetworkPolicy on Cilium clusters (GKE Dataplane V2),
// falls back to standard K8s NetworkPolicy otherwise.
func (pm *PolicyManager) EnsureNetworkPolicy(ctx context.Context, sandbox *karov1alpha1.SandboxClass, cni CNIType) error {
	logger := log.FromContext(ctx)

	if sandbox.Spec.NetworkPolicy.Egress == "" || sandbox.Spec.NetworkPolicy.Egress == "open" {
		// No restriction needed — clean up any existing policies.
		return pm.CleanupPolicies(ctx, sandbox)
	}

	if sandbox.Spec.NetworkPolicy.Egress == "none" {
		// Deny all egress.
		switch cni {
		case CNICilium:
			return pm.ensureCiliumDenyAll(ctx, sandbox)
		default:
			return pm.ensureStandardDenyAll(ctx, sandbox)
		}
	}

	// "restricted" — allow only specific domains and CIDRs.
	switch cni {
	case CNICilium:
		logger.Info("using CiliumNetworkPolicy for FQDN-based egress", "sandbox", sandbox.Name)
		return pm.ensureCiliumPolicy(ctx, sandbox)
	default:
		logger.Info("using standard NetworkPolicy with CIDR-based egress (no FQDN support)", "sandbox", sandbox.Name)
		return pm.ensureStandardPolicy(ctx, sandbox)
	}
}

// ensureCiliumPolicy creates a CiliumNetworkPolicy with toFQDNs rules for
// domain-based egress filtering. This is the preferred path on GKE Dataplane V2.
func (pm *PolicyManager) ensureCiliumPolicy(ctx context.Context, sandbox *karov1alpha1.SandboxClass) error {
	policyName := fmt.Sprintf("karo-sandbox-%s", sandbox.Name)

	// Build toFQDNs egress rules for allowed domains.
	var egressRules []interface{}

	// DNS egress — always allowed so FQDN resolution works.
	dnsRule := map[string]interface{}{
		"toEndpoints": []interface{}{
			map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"k8s:io.kubernetes.pod.namespace": "kube-system",
					"k8s:k8s-app":                    "kube-dns",
				},
			},
		},
		"toPorts": []interface{}{
			map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{"port": "53", "protocol": "UDP"},
					map[string]interface{}{"port": "53", "protocol": "TCP"},
				},
			},
		},
	}
	egressRules = append(egressRules, dnsRule)

	// FQDN-based egress rules.
	if len(sandbox.Spec.NetworkPolicy.AllowedDomains) > 0 {
		var fqdnSelectors []interface{}
		for _, domain := range sandbox.Spec.NetworkPolicy.AllowedDomains {
			fqdnSelectors = append(fqdnSelectors, map[string]interface{}{
				"matchName": domain,
			})
		}
		fqdnRule := map[string]interface{}{
			"toFQDNs": fqdnSelectors,
			"toPorts": []interface{}{
				map[string]interface{}{
					"ports": []interface{}{
						map[string]interface{}{"port": "443", "protocol": "TCP"},
						map[string]interface{}{"port": "80", "protocol": "TCP"},
					},
				},
			},
		}
		egressRules = append(egressRules, fqdnRule)
	}

	// CIDR-based egress rules.
	if len(sandbox.Spec.NetworkPolicy.AllowedCIDRs) > 0 {
		var cidrRules []interface{}
		for _, cidr := range sandbox.Spec.NetworkPolicy.AllowedCIDRs {
			cidrRules = append(cidrRules, map[string]interface{}{"cidr": cidr})
		}
		cidrRule := map[string]interface{}{
			"toCIDR": cidrRules,
		}
		egressRules = append(egressRules, cidrRule)
	}

	policy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      policyName,
				"namespace": sandbox.Namespace,
				"labels": map[string]interface{}{
					"karo.dev/sandbox-class": sandbox.Name,
					"karo.dev/managed-by":    "karo-operator",
				},
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         "karo.dev/v1alpha1",
						"kind":               "SandboxClass",
						"name":               sandbox.Name,
						"uid":                string(sandbox.UID),
						"controller":         true,
						"blockOwnerDeletion": true,
					},
				},
			},
			"spec": map[string]interface{}{
				"endpointSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"karo.dev/sandbox-class": sandbox.Name,
					},
				},
				"egress": egressRules,
			},
		},
	}

	return pm.applyUnstructured(ctx, policy)
}

// ensureStandardPolicy creates a standard K8s NetworkPolicy with CIDR-based
// egress rules. Domain names are resolved to IPs as a fallback.
func (pm *PolicyManager) ensureStandardPolicy(ctx context.Context, sandbox *karov1alpha1.SandboxClass) error {
	policyName := fmt.Sprintf("karo-sandbox-%s", sandbox.Name)

	// Build egress rules.
	var egressRules []networkingv1.NetworkPolicyEgressRule

	// DNS egress — always allowed.
	dnsPort := intstr.FromInt32(53)
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Port: &dnsPort, Protocol: &udp},
			{Port: &dnsPort, Protocol: &tcp},
		},
	})

	// Resolve allowed domains to IPs (fallback for standard NetworkPolicy).
	var ipBlocks []networkingv1.NetworkPolicyPeer
	for _, domain := range sandbox.Spec.NetworkPolicy.AllowedDomains {
		ips, err := net.LookupHost(domain)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			cidr := ip + "/32"
			if net.ParseIP(ip).To4() == nil {
				cidr = ip + "/128"
			}
			ipBlocks = append(ipBlocks, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: cidr},
			})
		}
	}

	// Add explicit CIDR allowlist.
	for _, cidr := range sandbox.Spec.NetworkPolicy.AllowedCIDRs {
		ipBlocks = append(ipBlocks, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: cidr},
		})
	}

	if len(ipBlocks) > 0 {
		httpsPort := intstr.FromInt32(443)
		httpPort := intstr.FromInt32(80)
		egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
			To: ipBlocks,
			Ports: []networkingv1.NetworkPolicyPort{
				{Port: &httpsPort, Protocol: &tcp},
				{Port: &httpPort, Protocol: &tcp},
			},
		})
	}

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				"karo.dev/sandbox-class": sandbox.Name,
				"karo.dev/managed-by":    "karo-operator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "karo.dev/v1alpha1",
					Kind:               "SandboxClass",
					Name:               sandbox.Name,
					UID:                sandbox.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"karo.dev/sandbox-class": sandbox.Name,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egressRules,
		},
	}

	var existing networkingv1.NetworkPolicy
	key := types.NamespacedName{Name: policyName, Namespace: sandbox.Namespace}
	if err := pm.client.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return pm.client.Create(ctx, policy)
		}
		return err
	}
	policy.ResourceVersion = existing.ResourceVersion
	return pm.client.Update(ctx, policy)
}

// ensureCiliumDenyAll creates a CiliumNetworkPolicy that denies all egress.
func (pm *PolicyManager) ensureCiliumDenyAll(ctx context.Context, sandbox *karov1alpha1.SandboxClass) error {
	policyName := fmt.Sprintf("karo-sandbox-%s", sandbox.Name)
	policy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      policyName,
				"namespace": sandbox.Namespace,
				"labels": map[string]interface{}{
					"karo.dev/sandbox-class": sandbox.Name,
					"karo.dev/managed-by":    "karo-operator",
				},
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         "karo.dev/v1alpha1",
						"kind":               "SandboxClass",
						"name":               sandbox.Name,
						"uid":                string(sandbox.UID),
						"controller":         true,
						"blockOwnerDeletion": true,
					},
				},
			},
			"spec": map[string]interface{}{
				"endpointSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"karo.dev/sandbox-class": sandbox.Name,
					},
				},
				// Empty egress array = deny all egress.
				"egressDeny": []interface{}{
					map[string]interface{}{},
				},
			},
		},
	}
	return pm.applyUnstructured(ctx, policy)
}

// ensureStandardDenyAll creates a standard NetworkPolicy that denies all egress.
func (pm *PolicyManager) ensureStandardDenyAll(ctx context.Context, sandbox *karov1alpha1.SandboxClass) error {
	policyName := fmt.Sprintf("karo-sandbox-%s", sandbox.Name)
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				"karo.dev/sandbox-class": sandbox.Name,
				"karo.dev/managed-by":    "karo-operator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "karo.dev/v1alpha1",
					Kind:               "SandboxClass",
					Name:               sandbox.Name,
					UID:                sandbox.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"karo.dev/sandbox-class": sandbox.Name,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			// No egress rules = deny all egress.
			Egress: []networkingv1.NetworkPolicyEgressRule{},
		},
	}

	var existing networkingv1.NetworkPolicy
	key := types.NamespacedName{Name: policyName, Namespace: sandbox.Namespace}
	if err := pm.client.Get(ctx, key, &existing); err != nil {
		if errors.IsNotFound(err) {
			return pm.client.Create(ctx, policy)
		}
		return err
	}
	policy.ResourceVersion = existing.ResourceVersion
	return pm.client.Update(ctx, policy)
}

// CleanupPolicies removes managed network policies when egress is "open" or on deletion.
func (pm *PolicyManager) CleanupPolicies(ctx context.Context, sandbox *karov1alpha1.SandboxClass) error {
	policyName := fmt.Sprintf("karo-sandbox-%s", sandbox.Name)

	// Try deleting CiliumNetworkPolicy.
	cilium := &unstructured.Unstructured{}
	cilium.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})
	cilium.SetName(policyName)
	cilium.SetNamespace(sandbox.Namespace)
	if err := pm.client.Delete(ctx, cilium); err != nil && !errors.IsNotFound(err) {
		// Ignore not-found and CRD-not-installed errors.
		if !errors.IsNotFound(err) {
			log.FromContext(ctx).V(1).Info("could not delete CiliumNetworkPolicy", "error", err)
		}
	}

	// Try deleting standard NetworkPolicy.
	stdPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: sandbox.Namespace,
		},
	}
	if err := pm.client.Delete(ctx, stdPolicy); err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}

// applyUnstructured creates or updates an unstructured resource.
func (pm *PolicyManager) applyUnstructured(ctx context.Context, obj *unstructured.Unstructured) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())

	key := types.NamespacedName{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
	if err := pm.client.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			return pm.client.Create(ctx, obj)
		}
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return pm.client.Update(ctx, obj)
}

func boolPtr(b bool) *bool { return &b }
