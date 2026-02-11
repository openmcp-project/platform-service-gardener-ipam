package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
)

// IPAMConfigSpec defines the desired state of IPAMConfig
type IPAMConfigSpec struct {
	// ParentCIDRs maps identifiers to a list of CIDRs that are consired the parent networks from which the individual CIDRs are sliced.
	// Note that already assigned CIDRs are not revoked if their parent network is removed from this list.
	ParentCIDRs map[string][]CIDR `json:"parentCIDRs"`

	// InjectionRules defines the rules for injecting CIDRs into Clusters.
	InjectionRules []CIDRInjection `json:"injectionRules,omitempty"`
}

// +kubebuilder:validation:Pattern=`^((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.?\b){4}\/(3[0-2]|(1|2|)[0-9])$`
type CIDR string

// CIDRInjection specifies injection rules for CIDRs.
type CIDRInjection struct {
	// ID is a unique identifier for this injection rule.
	// This is mainly required to avoid rolling out updates to already processed Clusters:
	// A Cluster will never have a rule with the same ID applied twice.
	// This means that if you modify an existing rule, it will only affect Clusters which have not been affected by this rule before.
	// To affect all Clusters matching the selectors, independent of what has been done to them before, you need to create a new rule with a new ID (or rename the existing one).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +required
	ID string `json:"id"`

	// IdentityPurposeSelector restricts which Clusters this injection rule applies to.
	clustersv1alpha1.IdentityLabelPurposeSelector `json:",inline"`

	// Injections describes a list of CIDR injections.
	Injections []SingleCIDRInjection `json:"injections"`
}

type SingleCIDRInjection struct {
	// ID is an optional identifier for the CIDR.
	// It can be used as value for 'parent' in a subsequent injection belonging to the same rule (the order of injections is important here).
	// +optional
	ID string `json:"id,omitempty"`
	// Path is the JSONPath expression on where to inject the CIDR.
	// This must either be a valid JSONPatch path expression or a JSONPath-like expression pointing to a single string field in the Cluster spec where the CIDR should be injected.
	// See https://github.com/openmcp-project/controller-utils/blob/main/docs/libs/jsonpatch.md#path-notation for details on the supported path notation.
	// +kubebuilder:validation:MinLength=1
	// +required
	Path string `json:"path"`
	// Parents is a list of identifiers from the parent CIDR mapping that are allowed to be the parent of this CIDR.
	// The list will be traversed in order, and the first parent CIDR with available capacity will be used as parent for this CIDR.
	// If this list is empty, all parent CIDRs defined in the config are allowed parents for this CIDR. They will be traversed in an undefined order.
	// Child CIDRs from the same list of injections (within the same CIDRInjection struct) that have been generated before can also be used here, if they have an ID assigned. They are not included if this value is empty.
	// +optional
	Parents []string `json:"parents,omitempty"`
	// SubnetSize is the desired size of the generated CIDR.
	// This is the part that will come after the '/'.
	// This value actually refers to the prefix bitmask length, so bigger numbers will result in smaller IP ranges.
	// The highest allowed value is 32, which will result in a range containing only a single IP address (the one before the '/').
	// Note that the size of child CIDRs needs to be smaller (= bigger number here) than the size of its parent CIDR.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=32
	// +required
	SubnetSize int `json:"subnetSize"`
}

// +kubebuilder:object:root=true
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
// +kubebuilder:resource:scope=Cluster,shortName=gicfg;gipamcfg

// IPAMConfig is the Schema for the Gardener IPAM PlatformService configuration API
type IPAMConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec IPAMConfigSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// IPAMConfigList contains a list of IPAMConfig
type IPAMConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPAMConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IPAMConfig{}, &IPAMConfigList{})
}
