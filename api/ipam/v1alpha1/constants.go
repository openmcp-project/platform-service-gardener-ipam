package v1alpha1

const (
	// ManagedPurposeLabelValue is the value for the managed purpose label.
	// It is used on ClusterConfig resources created by this controller.
	ManagedPurposeLabelValue = "gardener-ipam"

	// CIDRReleaseFinalizer is used on created ClusterConfig resources to ensure the CIDRs are released from the internal storage before the ClusterConfig gets deleted.
	CIDRReleaseFinalizer = "ipam." + GroupName + "/cidr-release"

	// ClusterTargetLabel is the label key used to indicate the target Cluster for a ClusterConfig.
	ClusterTargetLabel = "ipam." + GroupName + "/cluster"
	// InjectionRuleLabel is the label key used to indicate the injection rule for a ClusterConfig.
	InjectionRuleLabel = "ipam." + GroupName + "/injection-rule"
)
