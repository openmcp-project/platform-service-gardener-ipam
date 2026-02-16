package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	goipam "github.com/metal-stack/go-ipam"

	gardenv1alpha1 "github.com/openmcp-project/cluster-provider-gardener/api/core/v1alpha1"
	jsonpatchapi "github.com/openmcp-project/controller-utils/api/jsonpatch"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/controller-utils/pkg/collections"
	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	"github.com/openmcp-project/controller-utils/pkg/logging"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	openmcpconst "github.com/openmcp-project/openmcp-operator/api/constants"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
)

// ClusterConfigLabels returns the labels that should be set on a ClusterConfig for the given cluster and ruleID.
// For creating selectors, the cl as well as the ruleID arguments can be empty, in which case the returned labels will not contain the corresponding keys.
func ClusterConfigLabels(cl *clustersv1alpha1.Cluster, ruleID string) map[string]string {
	res := map[string]string{
		openmcpconst.ManagedByLabel:      ProviderName(),
		openmcpconst.ManagedPurposeLabel: ipamv1alpha1.ManagedPurposeLabelValue,
		openmcpconst.EnvironmentLabel:    Environment(),
	}
	if cl != nil {
		res[ipamv1alpha1.ClusterTargetLabel] = cl.Name
	}
	if ruleID != "" {
		res[ipamv1alpha1.InjectionRuleLabel] = ruleID
	}
	return res
}

// FetchRelevantClusters fetches all Cluster resources from the platform cluster and filters them depending on the config:
// - If the config is nil, it returns all clusters which for which at least one ClusterConfig managed by this controller exists.
// - If the config is not nil, it returns all clusters which match at least one of the injection rules defined in the config.
func FetchRelevantClusters(ctx context.Context, cfg *ipamv1alpha1.IPAMConfig, platformCluster *clusters.Cluster) ([]*clustersv1alpha1.Cluster, error) {
	log := logging.FromContextOrPanic(ctx)

	var relevantClusters []*clustersv1alpha1.Cluster
	if cfg == nil {
		// no config, fetch ClusterConfigs and return their owners
		ccs, err := FetchClusterConfigs(ctx, platformCluster)
		if err != nil {
			return nil, fmt.Errorf("error fetching ClusterConfigs: %w", err)
		}
		tmp := map[string]map[string]*clustersv1alpha1.Cluster{} // cluster namespace -> cluster name -> cluster, temporary cache to avoid fetching the same cluster multiple times
		var errs error
		for _, cc := range ccs {
			cName, ok := cc.Labels[ipamv1alpha1.ClusterTargetLabel]
			if !ok {
				log.Error(nil, "ClusterConfig is missing the cluster target label", "clusterConfig", fmt.Sprintf("%s/%s", cc.Namespace, cc.Name), "label", ipamv1alpha1.ClusterTargetLabel)
				continue
			}
			ns, ok := tmp[cc.Namespace]
			if !ok {
				ns = map[string]*clustersv1alpha1.Cluster{}
				tmp[cc.Namespace] = ns
			}
			if _, ok := ns[cName]; !ok { // nothing to do if the cluster is already fetched
				c := &clustersv1alpha1.Cluster{}
				if err := platformCluster.Client().Get(ctx, client.ObjectKey{Namespace: cc.Namespace, Name: cName}, c); err != nil {
					if !apierrors.IsNotFound(err) {
						errs = errors.Join(errs, fmt.Errorf("error fetching Cluster '%s' for ClusterConfig '%s/%s': %w", cName, cc.Namespace, cc.Name, err))
					}
				} else {
					ns[cName] = c
				}
			}
		}
		relevantClusters = collections.AggregateMap(tmp, func(_ string, clustersInNamespace map[string]*clustersv1alpha1.Cluster, res []*clustersv1alpha1.Cluster) []*clustersv1alpha1.Cluster {
			res = append(res, slices.Collect(maps.Values(clustersInNamespace))...)
			return res
		}, []*clustersv1alpha1.Cluster{})
	} else {
		allClusters := &clustersv1alpha1.ClusterList{}
		if err := platformCluster.Client().List(ctx, allClusters); err != nil {
			return nil, fmt.Errorf("unable to list Cluster resources: %w", err)
		}
		relevantClusters = []*clustersv1alpha1.Cluster{}

		for _, cluster := range allClusters.Items {
			// check for matching injection rules
			for _, rule := range cfg.Spec.InjectionRules {
				if rule.Matches(&cluster) {
					relevantClusters = append(relevantClusters, &cluster)
					break
				}
			}
		}
	}

	return relevantClusters, nil
}

// FetchClusterConfigs fetches all ClusterConfig resources that are managed by this controller according to their labels.
func FetchClusterConfigs(ctx context.Context, platformCluster *clusters.Cluster) ([]*gardenv1alpha1.ClusterConfig, error) {
	ccs := &gardenv1alpha1.ClusterConfigList{}
	if err := platformCluster.Client().List(ctx, ccs, client.MatchingLabels(ClusterConfigLabels(nil, ""))); err != nil {
		return nil, fmt.Errorf("error listing ClusterConfigs: %w", err)
	}
	return collections.ProjectSliceToSlice(ccs.Items, func(cc gardenv1alpha1.ClusterConfig) *gardenv1alpha1.ClusterConfig {
		return &cc
	}), nil
}

// FetchClusterConfigsForCluster fetches all ClusterConfig resources created by the IPAM controller for the given cluster.
// It returns them as a mapping of ruleID to ClusterConfig, where the ruleID is taken from the InjectionRuleLabel on the ClusterConfig.
func FetchClusterConfigsForCluster(ctx context.Context, platformCluster *clusters.Cluster, cl *clustersv1alpha1.Cluster) (map[string]*gardenv1alpha1.ClusterConfig, error) {
	ccs := &gardenv1alpha1.ClusterConfigList{}
	if err := platformCluster.Client().List(ctx, ccs, client.MatchingLabels(ClusterConfigLabels(cl, "")), client.InNamespace(cl.Namespace)); err != nil {
		return nil, err
	}
	return collections.ProjectSliceToMap(ccs.Items, func(cc gardenv1alpha1.ClusterConfig) (string, *gardenv1alpha1.ClusterConfig) {
		return cc.Labels[ipamv1alpha1.InjectionRuleLabel], &cc
	}), nil
}

// GenerateClusterConfigForInjectionList generates a ClusterConfig resource for the given cluster, injection rule ID and list of injections (mapping from JSONPath to CIDR).
func GenerateClusterConfigForInjectionList(cl *clustersv1alpha1.Cluster, ruleID string, injections map[string]string) (*gardenv1alpha1.ClusterConfig, error) {
	ccName, err := GenerateClusterConfigName(cl, ruleID)
	if err != nil {
		return nil, err
	}
	cc := &gardenv1alpha1.ClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ccName,
			Namespace: cl.Namespace,
			Labels:    ClusterConfigLabels(cl, ruleID),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         ipamv1alpha1.GroupVersion.String(),
					Kind:               "Cluster",
					Name:               cl.Name,
					UID:                cl.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(false),
				},
			},
			Finalizers: []string{
				ipamv1alpha1.CIDRManagementFinalizer,
			},
		},
	}

	cc.Spec.PatchOptions = &gardenv1alpha1.PatchOptions{
		CreateMissingOnAdd: ptr.To(true),
	}
	// add patches for all injections
	for path, cidr := range injections {
		cidrJson, err := json.Marshal(cidr)
		if err != nil {
			return nil, fmt.Errorf("error marshaling CIDR '%s' for path '%s' to JSON: %w", cidr, path, err)
		}
		cc.Spec.Patches = append(cc.Spec.Patches, jsonpatchapi.JSONPatch{
			Op:   jsonpatchapi.ADD,
			Path: path,
			Value: &apiextensionsv1.JSON{
				Raw: cidrJson,
			},
		})
	}

	return cc, nil
}

// GenerateClusterConfigName is used to generate the name for a ClusterConfig resource based on the cluster and the ruleID.
// The name is generated as "<cluster-name>--ipam--<ruleID>", but shortened if it exceeds the Kubernetes name length limit.
func GenerateClusterConfigName(cl *clustersv1alpha1.Cluster, ruleID string) (string, error) {
	if cl == nil || ruleID == "" {
		return "", fmt.Errorf("cluster and ruleID must not be nil or empty")
	}
	ccName, err := ctrlutils.ShortenToXCharacters(fmt.Sprintf("%s--ipam--%s", cl.Name, ruleID), ctrlutils.K8sMaxNameLength)
	if err != nil {
		return "", fmt.Errorf("error generating name for ClusterConfig: %w", err)
	}
	return ccName, nil
}

// ReleaseCIDRsForClusterConfig tries to release all CIDRs specified in the patches of the given ClusterConfig.
// This is meant to be used on ClusterConfigs that are in deletion, but still have the CIDR management finalizer.
// No-op if the ClusterConfig does not have the CIDR management finalizer, as then the CIDRs could already have been released and taken by another ClusterConfig
// and releasing them again could cause conflicts.
// After releasing the CIDRs, the CIDR management finalizer is removed from the ClusterConfigs. Failing to do so causes an error and prevents the CIDRs from being released.
//
// Note that this function is atomic: If an error occurs, none of the CIDRs from the ClusterConfig are released. Otherwise, all of them are released.
// This is achieved by modifying a copy of the internal IPAM state and only swapping it with the real internal state if everything has worked.
func ReleaseCIDRsForClusterConfig(ctx context.Context, platformCluster *clusters.Cluster, cc *gardenv1alpha1.ClusterConfig) error {
	IPAM.lock.Lock()
	defer IPAM.lock.Unlock()

	return releaseCIDRsForClusterConfig_internal(ctx, platformCluster, cc)
}

// releaseCIDRsForClusterConfig_internal is the internal version of ReleaseCIDRsForClusterConfig.
// It assumes that the caller holds the ipam lock.
func releaseCIDRsForClusterConfig_internal(ctx context.Context, platformCluster *clusters.Cluster, cc *gardenv1alpha1.ClusterConfig) error {
	log := logging.FromContextOrPanic(ctx)

	// create a copy of the internal IPAM instance to make this operation atomic
	ipamData, err := IPAM.internal.Dump(ctx)
	if err != nil {
		return fmt.Errorf("error dumping IPAM state: %w", err)
	}
	newIpam := goipam.New(ctx)
	if err := newIpam.Load(ctx, ipamData); err != nil {
		return fmt.Errorf("error loading IPAM state into new instance: %w", err)
	}

	for i, patch := range cc.Spec.Patches {
		if patch.Value == nil {
			log.Debug("Skipping patch due to empty value", "index", i)
			continue
		}
		var cidr string
		if err := json.Unmarshal(patch.Value.Raw, &cidr); err != nil {
			return fmt.Errorf("error unmarshaling value of patch %d (path '%s'): %w", i, patch.Path, err)
		}
		ptr, err := newIpam.PrefixFrom(ctx, cidr)
		if err != nil && !errors.Is(err, goipam.ErrNotFound) {
			return fmt.Errorf("error fetching prefix for CIDR '%s': %w", cidr, err)
		}
		if ptr != nil {
			if err := newIpam.ReleaseChildPrefix(ctx, ptr); err != nil && !errors.Is(err, goipam.ErrNotFound) {
				return fmt.Errorf("unable to release child prefix %s: %w", ptr.Cidr, err)
			}
		}
	}
	// remove the CIDR management finalizer
	old := cc.DeepCopy()
	if controllerutil.RemoveFinalizer(cc, ipamv1alpha1.CIDRManagementFinalizer) {
		if err := platformCluster.Client().Patch(ctx, cc, client.MergeFrom(old)); err != nil {
			return fmt.Errorf("error removing CIDR management finalizer from ClusterConfig '%s/%s': %w", cc.Namespace, cc.Name, err)
		}
	}

	// everything has worked, so swap out the old internal IPAM instance with the new one that has the relevant CIDRs released
	IPAM.internal = newIpam

	return nil
}
