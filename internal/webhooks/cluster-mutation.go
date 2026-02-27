package webhooks

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openmcp-project/controller-utils/pkg/logging"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"

	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

const ReferenceInjectorName = "ReferenceInjector"

// ReferenceInjector is a mutating webhook which injects ClusterConfig references for all rules in the config which match the cluster.
type ReferenceInjector struct{}

var _ admission.Defaulter[*clustersv1alpha1.Cluster] = &ReferenceInjector{}

func (c *ReferenceInjector) SetupWebhookWithManager(ctx context.Context, mgr ctrl.Manager) error {
	ri := &ReferenceInjector{}

	return ctrl.NewWebhookManagedBy(mgr, &clustersv1alpha1.Cluster{}).
		WithDefaulter(ri).
		Complete()
}

// Default implements [admission.Defaulter].
func (c *ReferenceInjector) Default(ctx context.Context, cluster *clustersv1alpha1.Cluster) error {
	log := logging.FromContextOrDiscard(ctx).WithName(ReferenceInjectorName)
	log.Debug("Mutating cluster")

	// get config to check which injection rules apply to this cluster
	cfg := shared.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config not found, unable to inject ClusterConfig references into Cluster")
	}

	// generate list of ClusterConfig references to inject into the cluster based on the config
	refs := map[string]string{} // maps reference name to rule ID, used for logging purposes
	for _, rule := range cfg.Spec.InjectionRules {
		if rule.Matches(cluster) {
			name, err := shared.GenerateClusterConfigName(cluster, rule.ID)
			if err != nil {
				return fmt.Errorf("failed to generate ClusterConfig name for rule '%s': %w", rule.ID, err)
			}
			refs[name] = rule.ID
		}
	}

	// ensure that the generated references are present in the cluster's reference list
	for ref, ruleID := range refs {
		found := false
		for _, existing := range cluster.Spec.ClusterConfigs {
			if existing.Name == ref {
				found = true
				break
			}
		}
		if !found {
			log.Debug("Adding new ClusterConfig reference to cluster", "reference", ref, "ruleID", ruleID)
			cluster.Spec.ClusterConfigs = append(cluster.Spec.ClusterConfigs, commonapi.LocalObjectReference{Name: ref})
		}
	}

	return nil
}
