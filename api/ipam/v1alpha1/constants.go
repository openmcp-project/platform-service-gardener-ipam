package v1alpha1

const (
	// ManagedPurposeLabelValue is the value for the managed purpose label.
	// It is used on ClusterConfig resources created by this controller.
	ManagedPurposeLabelValue = "gardener-ipam"

	// CIDRManagementFinalizer is used on created ClusterConfig resources to ensure the CIDRs are released from the internal storage before the ClusterConfig gets deleted.
	CIDRManagementFinalizer = "ipam." + GroupName + "/cidr-management"

	// ClusterTargetLabel is the label key used to indicate the target Cluster for a ClusterConfig.
	ClusterTargetLabel = "ipam." + GroupName + "/cluster"
	// InjectionRuleLabel is the label key used to indicate the injection rule for a ClusterConfig.
	InjectionRuleLabel = "ipam." + GroupName + "/injection-rule"
)
