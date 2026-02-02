package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
)

// GardenerIPAMConfigSpec defines the desired state of GardenerIPAMConfig
type GardenerIPAMConfigSpec struct {
	// ParentCIDRs maps identifiers to a list of CIDRs that are consired the parent networks from which the individual CIDRs are sliced.
	// Note that already assigned CIDRs are not revoked if their parent network is removed from this list.
	ParentCIDRs map[string][]CIDR `json:"parentCIDRs"`
}

// +kubebuilder:validation:Pattern=`^((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.?\b){4}\/(3[0-2]|(1|2|)[0-9])$`
type CIDR string

// CIDRInjection specifies injection rules for CIDRs.
type CIDRInjection struct {
	// IdentityPurposeSelector restricts which Clusters this injection rule applies to.
	clustersv1alpha1.IdentityLabelPurposeSelector `json:",inline"`

	// Injections describes a list of CIDR injections.
	Injections []SingleCIDRInjection `json:"injections"`
}

type SingleCIDRInjection struct {
	// ID is an optional identifier for the CIDR.
	// It can be used as value for 'parent' in a subsequent injection (the order of injections is important here).
	// +optional
	ID string `json:"id,omitempty"`
	// Path is the JSONPath expression on where to inject the CIDR.
	// +kubebuilder:validation:MinLength=1
	// +required
	Path string `json:"path"`
	// Parent references the identifier of the parent CIDR.
	// +kubebuilder:validation:MinLength=1
	// +required
	Parent string `json:"parent"`
	// SubnetSize is the desired size of the generated CIDR.
	// This is the part that will come after the '/'.
	// This value actually refers to the prefix bitmask length, so bigger numbers will result in smaller IP ranges.
	// The highest allowed value is 32, which will result in a range containing only a single IP address (the one before the '/').
	// Note that the size of child CIDRs needs to be smaller (= bigger number here) or equal to the size of its parent CIDR. If equal, the child CIDR will consume the whole parent CIDR.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=32
	// +required
	SubnetSize int `json:"subnetSize"`
}

// +kubebuilder:object:root=true
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
// +kubebuilder:resource:scope=Cluster,shortName=gicfg;gipamcfg

// GardenerIPAMConfig is the Schema for the DNS PlatformService configuration API
type GardenerIPAMConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GardenerIPAMConfigSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// GardenerIPAMConfigList contains a list of GardenerIPAMConfig
type GardenerIPAMConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GardenerIPAMConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GardenerIPAMConfig{}, &GardenerIPAMConfigList{})
}
