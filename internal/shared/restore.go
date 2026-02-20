package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	goipam "github.com/metal-stack/go-ipam"

	gardenv1alpha1 "github.com/openmcp-project/cluster-provider-gardener/api/core/v1alpha1"
	jsonpatchapi "github.com/openmcp-project/controller-utils/api/jsonpatch"
	"github.com/openmcp-project/controller-utils/pkg/logging"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
)

type RestorationInstruction struct {
	ClusterConfigsToCreate []*gardenv1alpha1.ClusterConfig
	ClusterConfigsToUpdate []*gardenv1alpha1.ClusterConfig
}

// Size returns the number of ClusterConfigs to create and update as specified in the instruction.
// The first argument is the number of ClusterConfigs to create, the second argument is the number of ClusterConfigs to update.
func (ri *RestorationInstruction) Size() (int, int) {
	if ri == nil {
		return 0, 0
	}
	return len(ri.ClusterConfigsToCreate), len(ri.ClusterConfigsToUpdate)
}

// Apply acts on the instructions and creates/updates the ClusterConfigs as specified.
func (ri *RestorationInstruction) Apply(ctx context.Context, platformCluster client.Client) error {
	if ri == nil {
		return nil
	}
	var errs error
	for _, cc := range ri.ClusterConfigsToCreate {
		if err := platformCluster.Create(ctx, cc); err != nil {
			errs = errors.Join(errs, fmt.Errorf("error creating ClusterConfig '%s/%s': %w", cc.Namespace, cc.Name, err))
		}
	}
	for _, cc := range ri.ClusterConfigsToUpdate {
		if err := platformCluster.Update(ctx, cc); err != nil {
			errs = errors.Join(errs, fmt.Errorf("error updating ClusterConfig '%s/%s': %w", cc.Namespace, cc.Name, err))
		}
	}
	if errs != nil {
		return fmt.Errorf("one or more errors occurred trying to create/update ClusterConfigs:\n%w", errs)
	}
	return nil
}

// CheckClusterConfigsForCluster uses the appliedRules annotation on the Cluster to compare the expected ClusterConfigs with the actual ones.
// It returns a list of ClusterConfigs that need to be created (first return value) and updated (second return value) in order to restore the expected state.
// Note that this function does not actually perform the create/update operations, it just prepares the ClusterConfig objects with the necessary changes.
// Both the appliedRules and the ccs arguments may be nil, in which case they will be fetched by the function. The ccs argument is expected to be a mapping from ruleID to ClusterConfig.
// ClusterConfigs which are missing the CIDR management finalizer will be added to the update list, unless they are in deletion.
func CheckClusterConfigsForCluster(ctx context.Context, platformCluster client.Client, cl *clustersv1alpha1.Cluster, appliedRules ipamv1alpha1.AppliedRulesAnnotation, ccs map[string]*gardenv1alpha1.ClusterConfig) (*RestorationInstruction, error) {
	log := logging.FromContextOrPanic(ctx)

	// fetch appliedRules if not provided
	if appliedRules == nil {
		log.Debug("Parsing applied rules annotation")
		appliedRulesString, ok := cl.Annotations[ipamv1alpha1.AppliedRulesAnnotationKey]
		appliedRules = ipamv1alpha1.AppliedRulesAnnotation{}
		if ok {
			if err := appliedRules.FromAnnotation(appliedRulesString); err != nil {
				return nil, fmt.Errorf("unable to recover applied rules: %w", err)
			}
		}
		log.Debug("Successfully parsed applied rules annotation", "annotation", appliedRulesString)
	}

	// fetch ClusterConfigs if not provided
	if ccs == nil {
		log.Debug("Fetching ClusterConfigs for cluster")
		var err error
		ccs, err = FetchClusterConfigsForCluster(ctx, platformCluster, cl)
		if err != nil {
			return nil, fmt.Errorf("unable to fetch ClusterConfigs for cluster: %w", err)
		}
		log.Debug("Successfully fetched ClusterConfigs for cluster", "count", len(ccs))
	}

	// identify missing injections
	// Basically, for each rule mentioned in appliedRules, we identify the corresponding ClusterConfig, if any, and check if all injections from appliedRules are present there.
	missingInjections := map[string]map[string]string{} // ruleID -> path -> CIDR
	for ruleID, injections := range appliedRules {
		// identify corresponding ClusterConfig
		cc := ccs[ruleID]
		if cc == nil {
			// no ClusterConfig found, all injections are missing
			mi := missingInjections[ruleID]
			if mi == nil {
				mi = map[string]string{}
			}
			maps.Copy(mi, injections)
			missingInjections[ruleID] = mi
			continue
		}
		// ClusterConfig found, check for missing injections
		mi := missingInjections[ruleID]
		if mi == nil {
			mi = map[string]string{}
		}
		for path, cidr := range injections {
			found := false
			for _, patch := range cc.Spec.Patches {
				if patch.Path == path {
					found = true
					break
				}
			}
			if !found {
				mi[path] = cidr
			}
		}
		missingInjections[ruleID] = mi
		log.Debug("Identified missing injections for rule", "id", ruleID, "missingPaths", sets.List(sets.KeySet(mi)))
	}

	// update ClusterConfigs accordingly to the identified missing injections
	newCCs := map[string]*gardenv1alpha1.ClusterConfig{}
	updatedCCs := map[string]*gardenv1alpha1.ClusterConfig{}
	var errs error
	for ruleID, injections := range missingInjections {
		if len(injections) == 0 {
			continue
		}
		cc := ccs[ruleID]
		if cc == nil {
			// no ClusterConfig exists, create new one
			newCC, err := GenerateClusterConfigForInjectionList(cl, ruleID, injections)
			if err != nil {
				errs = errors.Join(errs, fmt.Errorf("error generating ClusterConfig for rule '%s': %w", ruleID, err))
				continue
			}
			newCCs[ruleID] = newCC
		} else {
			// ClusterConfig exists, add missing injections to it
			cc = cc.DeepCopy()
			for path, cidr := range injections {
				cidrJson, err := json.Marshal(cidr)
				if err != nil {
					errs = errors.Join(errs, fmt.Errorf("error marshaling CIDR '%s' for path '%s' in rule '%s' to JSON: %w", cidr, path, ruleID, err))
					continue
				}
				cc.Spec.Patches = append(cc.Spec.Patches, jsonpatchapi.JSONPatch{
					Op:   jsonpatchapi.ADD,
					Path: path,
					Value: &apiextensionsv1.JSON{
						Raw: cidrJson,
					},
				})
			}
			updatedCCs[ruleID] = cc
		}
	}
	if errs != nil {
		return nil, fmt.Errorf("one or more errors occurred trying to generate ClusterConfigs for missing injections:\n%w", errs)
	}

	// check if any ClusterConfig is missing the CIDR management finalizer
	for ruleID, cc := range ccs {
		if !slices.Contains(cc.GetFinalizers(), ipamv1alpha1.CIDRManagementFinalizer) && cc.GetDeletionTimestamp().IsZero() {
			// finalizer is missing and the ClusterConfig is not in deletion, add it to the update list
			newCC, ok := updatedCCs[ruleID]
			if !ok {
				newCC = cc.DeepCopy()
			}
			if controllerutil.AddFinalizer(newCC, ipamv1alpha1.CIDRManagementFinalizer) {
				updatedCCs[ruleID] = newCC
			}
		}
	}

	return &RestorationInstruction{
		ClusterConfigsToCreate: slices.Collect(maps.Values(newCCs)),
		ClusterConfigsToUpdate: slices.Collect(maps.Values(updatedCCs)),
	}, nil
}

// RestoreIPAMFromClusterState is meant to be used for initializing the shared IPAM state.
// It fetches all relevant Clusters and ClusterConfigs from the platform cluster, restores missing ClusterConfigs based on the appliedRules annotation on the cluster resources,
// and overwrites the shared IPAM state based on the existing ClusterConfigs.
// To avoid any inconsistencies, this function holds the ipam lock for the entire duration, meaning no other operations that need the IPAM state can run in parallel.
func RestoreIPAMFromClusterState(ctx context.Context, platformCluster client.Client) error {
	log := logging.FromContextOrPanic(ctx).WithName("Restore")
	ctx = logging.NewContext(ctx, log)

	log.Info("Restoring IPAM state from cluster state")

	// initialize IPAM if not already initialized
	// Technically, this should also be wrapped in a lock, but conflicts are unlikely here.
	if IPAM == nil {
		IPAM = NewIpam()
	}

	// get lock
	IPAM.lock.Lock()
	defer IPAM.lock.Unlock()

	// get config
	var cfg *ipamv1alpha1.IPAMConfig
	if config == nil {
		// this can happen if this method is called during startup, before the config controller had the chance to fetch the config
		cfg = &ipamv1alpha1.IPAMConfig{}
		log.Debug("Config not yet loaded, loading config")
		if err := platformCluster.Get(ctx, types.NamespacedName{Name: ProviderName()}, cfg); err != nil {
			return fmt.Errorf("error loading config: %w", err)
		}
	} else {
		cfg = config.DeepCopy()
	}

	// fetch all relevant clusters
	clusters, err := FetchRelevantClusters(ctx, cfg, platformCluster)
	if err != nil {
		return fmt.Errorf("error fetching Cluster resources for IPAM state restoration: %w", err)
	}
	log.Debug("Successfully fetched relevant Cluster resources for IPAM state restoration", "count", len(clusters))

	// fetch all ClusterConfig resources managed by this controller
	ccs, err := FetchClusterConfigs(ctx, platformCluster)
	if err != nil {
		return fmt.Errorf("error fetching ClusterConfig resources for IPAM state restoration: %w", err)
	}
	log.Debug("Successfully fetched relevant ClusterConfig resources for IPAM state restoration", "count", len(ccs))

	// verify integrity of each Cluster and restore its state
	var errs error
	handledCCs := map[string]*gardenv1alpha1.ClusterConfig{}
	for _, cluster := range clusters {
		// to avoid having to fetch the ClusterConfigs again, filter the existing list
		// since each of these ClusterConfigs belongs only to a single cluster, we can also remove it from the list afterwards
		newCCs := make([]*gardenv1alpha1.ClusterConfig, 0, len(ccs))
		clusterCCs := make(map[string]*gardenv1alpha1.ClusterConfig)
		for _, cc := range ccs {
			if cc.Namespace == cluster.Namespace && cc.Labels[ipamv1alpha1.ClusterTargetLabel] == cluster.Name {
				clusterCCs[cc.Labels[ipamv1alpha1.InjectionRuleLabel]] = cc
				handledCCs[objectKey(cc)] = cc
			} else {
				newCCs = append(newCCs, cc)
			}
		}
		ccs = newCCs

		ir, err := CheckClusterConfigsForCluster(ctx, platformCluster, cluster, nil, clusterCCs)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("error checking ClusterConfigs for Cluster '%s/%s': %w", cluster.Namespace, cluster.Name, err))
			continue
		}
		if createCount, updateCount := ir.Size(); createCount > 0 || updateCount > 0 {
			log.Info("Restoring ClusterConfigs", "cluster", fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Name), "createCount", createCount, "updateCount", updateCount)
			if err := ir.Apply(ctx, platformCluster); err != nil {
				errs = errors.Join(errs, fmt.Errorf("error restoring ClusterConfigs for Cluster '%s/%s': %w", cluster.Namespace, cluster.Name, err))
				continue
			}
			// the cluster state changed, let's fetch the ClusterConfigs for the Cluster again to make sure we have the latest state for the next steps
			updatedCCs, err := FetchClusterConfigsForCluster(ctx, platformCluster, cluster)
			if err != nil {
				errs = errors.Join(errs, fmt.Errorf("error fetching ClusterConfigs for Cluster '%s/%s' after update: %w", cluster.Namespace, cluster.Name, err))
				continue
			}
			for _, cc := range updatedCCs {
				handledCCs[objectKey(cc)] = cc
			}
		}
	}
	if errs != nil {
		return fmt.Errorf("one or more errors occurred during integrity check of ClusterConfigs:\n%w", errs)
	}

	// handle orphaned ClusterConfigs
	// All ClusterConfigs that are still part of the 'ccs' list have not been handled, which can happen due to one of two reasons:
	// - The selector in the config has changed and the corresponding Cluster was selected before but is not any more. In this case, the method for handling orphaned ClusterConfigs will detect the existing Cluster and do nothing.
	// - The corresponding Cluster has been deleted, but for some reason, the ClusterConfig still exists. In this case, the method for handling orphaned ClusterConfigs should clean up the ClusterConfig and free the CIDRs in the IPAM state.
	if len(ccs) > 0 {
		log.Info("Some ClusterConfigs could not be assigned to a Cluster and are potentially orphaned, triggering cleanup", "count", len(ccs))
		remainingCCs, err := handleOrphanedClusterConfigs_internal(ctx, platformCluster, ccs...)
		if err != nil {
			return fmt.Errorf("error handling orphaned ClusterConfigs: %w", err)
		}
		log.Debug("Handled potentially orphaned ClusterConfigs", "remainingClusterConfigs", len(remainingCCs))

		// if there are remaining ClusterConfigs with the CIDR management finalizer left, we must consider their CIDRs as still allocated and add them to the IPAM state
		for _, cc := range remainingCCs {
			if slices.Contains(cc.GetFinalizers(), ipamv1alpha1.CIDRManagementFinalizer) {
				handledCCs[objectKey(cc)] = cc
			}
		}
	}

	log.Debug("Building IPAM state from ClusterConfigs")
	ipam, err := ipamFromClusterConfigs(ctx, cfg, slices.Collect(maps.Values(handledCCs)))
	if err != nil {
		return fmt.Errorf("error building IPAM state from ClusterConfigs: %w", err)
	}

	log.Info("Successfully restored IPAM state")
	// overwrite internal IPAM state with the restored one
	IPAM.internal = ipam

	return nil
}

// ipamFromClusterConfigs evaluates the given ClusterConfigs and returns a new ipam state based on the CIDRs from the ClusterConfigs' patches.
func ipamFromClusterConfigs(ctx context.Context, cfg *ipamv1alpha1.IPAMConfig, ccs []*gardenv1alpha1.ClusterConfig) (goipam.Ipamer, error) {
	ipam := goipam.New(ctx)

	// register parent CIDRs from config
	parents := []*goipam.Prefix{}
	for id, parentList := range cfg.Spec.ParentCIDRs {
		for idx, parent := range parentList {
			// to avoid issues with duplicates, first verify that the CIDR is not yet registered before trying to register it
			p, err := fetchOrNewPrefix(ctx, ipam, string(parent))
			if err != nil {
				return nil, fmt.Errorf("error registering parent prefix '%s' from config for id '%s' at index %d: %w", parent, id, idx, err)
			}
			parents = append(parents, p)
		}
	}

	// register allocated CIDRs from ClusterConfigs
	for _, cc := range ccs {
		tmpParents := []*goipam.Prefix{}
		for idx, patch := range cc.Spec.Patches {
			if patch.Op != jsonpatchapi.ADD {
				// this should not happen
				continue
			}
			var cidr string
			if err := json.Unmarshal(patch.Value.Raw, &cidr); err != nil {
				return nil, fmt.Errorf("error unmarshaling CIDR from ClusterConfig '%s/%s' patch path '%s' (indes %d): %w", cc.Namespace, cc.Name, patch.Path, idx, err)
			}
			// figure out the parent CIDR
			parent, err := identifyParentPrefix(cidr, reverse(tmpParents), parents)
			if err != nil {
				return nil, fmt.Errorf("error identifying parent CIDR for '%s' (patch index %d): %w", cidr, idx, err)
			}
			var child *goipam.Prefix
			if parent == nil {
				// the CIDR does not have a registered parent
				// This can happen if a parent CIDR got removed from the config after it has already been used for generating child CIDRs.
				// In this case, we register the CIDR in the ipam state, but don't add it to the parent list, as it should not have any children outside of the following patches (which will be handled by the tmpParents list).
				_, err = fetchOrNewPrefix(ctx, ipam, cidr)
				if err != nil {
					return nil, fmt.Errorf("error registering parentless prefix '%s' (patch index %d): %w", cidr, idx, err)
				}
			} else {
				// the CIDR has a registered parent, so let's register it as a child of the parent
				child, err = ipam.AcquireSpecificChildPrefix(ctx, parent.Cidr, cidr)
				if err != nil {
					return nil, fmt.Errorf("error registering child prefix '%s' under parent '%s' (patch index %d): %w", cidr, parent.Cidr, idx, err)
				}
				tmpParents = append(tmpParents, child)
			}
		}
	}

	return ipam, nil
}

// identifyParentPrefix traverses all possible parents and returns the first one that overlaps with the given child CIDR.
// Returns nil and no error if no parent is found.
func identifyParentPrefix(child string, parentLists ...[]*goipam.Prefix) (*goipam.Prefix, error) {
	for _, parents := range parentLists {
		// This is ugly, but unfortunately, the ipam library's 'PrefixesOverlapping' function does return the overlapping CIDR only as part of the returned error message.
		for _, parent := range parents {
			if err := goipam.PrefixesOverlapping([]string{parent.Cidr}, []string{child}); err != nil {
				if !strings.Contains(err.Error(), "overlaps") {
					return nil, fmt.Errorf("error checking for overlapping prefixes between parent prefix '%s' and child CIDR '%s': %w", parent.Cidr, child, err)
				}
				// we found the parent CIDR
				return parent, nil
			}
		}
	}

	return nil, nil
}

// reverse returns a new slice with the elements of the input slice in reverse order.
func reverse[T any](src []T) []T {
	l := len(src)
	res := make([]T, l)
	for i, v := range src {
		res[l-1-i] = v
	}
	return res
}

// fetchOrNewPrefix retrieves a top-level prefix from the ipam state for the given CIDR.
// If the prefix does not exist, it is created and returned.
func fetchOrNewPrefix(ctx context.Context, ipam goipam.Ipamer, cidr string) (*goipam.Prefix, error) {
	p, err := ipam.PrefixFrom(ctx, cidr)
	if err != nil {
		if !errors.Is(err, goipam.ErrNotFound) {
			return nil, fmt.Errorf("error checking for existing parent prefix '%s': %w", cidr, err)
		}
		// not found, register it
		p, err = ipam.NewPrefix(ctx, cidr)
		if err != nil {
			return nil, fmt.Errorf("error registering parent prefix '%s': %w", cidr, err)
		}
	}
	return p, nil
}

// HandleOrphanedClusterConfigs traverses the given ClusterConfigs and checks if the corresponding Cluster still exists.
// If the Cluster exists, nothing needs to be done, as the ClusterConfig is not orphaned.
// Otherwise, it deletes the ClusterConfig, tries to free all CIDRs in the ClusterConfig from the IPAM state and removes the finalizer if successful.
// It returns a list of ClusterConfigs that still exist after the operation, meaning they were not orphaned, their CIDRs could not be freed, or they had one or more finalizers remaining after having the CIDR management one removed.
func HandleOrphanedClusterConfigs(ctx context.Context, platformCluster client.Client, ccs ...*gardenv1alpha1.ClusterConfig) ([]*gardenv1alpha1.ClusterConfig, error) {
	IPAM.lock.Lock()
	defer IPAM.lock.Unlock()

	return handleOrphanedClusterConfigs_internal(ctx, platformCluster, ccs...)
}

// handleOrphanedClusterConfigs_internal is the internal version of HandleOrphanedClusterConfigs.
// It expects the caller to hold the IPAM lock.
func handleOrphanedClusterConfigs_internal(ctx context.Context, platformCluster client.Client, ccs ...*gardenv1alpha1.ClusterConfig) ([]*gardenv1alpha1.ClusterConfig, error) {
	log := logging.FromContextOrPanic(ctx)
	log.Debug("Handling orphaned ClusterConfig resources ...")

	remaining := []*gardenv1alpha1.ClusterConfig{}

	var errs error
	checkedClusters := map[string]*clustersv1alpha1.Cluster{} // cache to avoid fetching the same cluster multiple times, key is "namespace/name"
	for _, cc := range ccs {
		clog := log.WithValues("clusterConfig", fmt.Sprintf("%s/%s", cc.Namespace, cc.Name))
		// check if corresponding Cluster exists
		cName := cc.Labels[ipamv1alpha1.ClusterTargetLabel]
		if cName == "" {
			errs = errors.Join(errs, fmt.Errorf("ClusterConfig '%s/%s' is missing the cluster target label, cannot check for orphaned state", cc.Namespace, cc.Name))
			continue
		}
		var cluster *clustersv1alpha1.Cluster
		if c, ok := checkedClusters[objectKeyFromStrings(cc.Namespace, cName)]; ok {
			cluster = c
		} else {
			cluster = &clustersv1alpha1.Cluster{}
			if err := platformCluster.Get(ctx, types.NamespacedName{Namespace: cc.Namespace, Name: cName}, cluster); err != nil {
				if !apierrors.IsNotFound(err) {
					errs = errors.Join(errs, fmt.Errorf("error checking existence of Cluster '%s/%s' for ClusterConfig '%s/%s': %w", cc.Namespace, cName, cc.Namespace, cc.Name, err))
				}
				cluster = nil
			}
		}

		if cluster != nil {
			// Cluster exists, ClusterConfig is not orphaned, nothing to do
			clog.Debug("ClusterConfig is not orphaned, corresponding Cluster exists", "cluster", fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Name))
			remaining = append(remaining, cc)
			continue
		}

		// trigger deletion, if not already triggered
		if cc.GetDeletionTimestamp().IsZero() {
			if err := platformCluster.Delete(ctx, cc); client.IgnoreNotFound(err) != nil {
				errs = errors.Join(errs, fmt.Errorf("error deleting orphaned ClusterConfig '%s/%s': %w", cc.Namespace, cc.Name, err))
				remaining = append(remaining, cc)
				continue
			}
		} else {
			clog.Debug("ClusterConfig is already in deletion")
		}

		// if the ClusterConfig still has the IPAM finalizer, try to release the CIDRs and remove the finalizer
		if slices.Contains(cc.GetFinalizers(), ipamv1alpha1.CIDRManagementFinalizer) {
			clog.Debug("CIDR management finalizer is still present on ClusterConfig, trying to release CIDRs ...")
			if err := releaseCIDRsForClusterConfig_internal(ctx, platformCluster, cc); err != nil {
				errs = errors.Join(errs, fmt.Errorf("error releasing CIDRs for orphaned ClusterConfig '%s/%s': %w", cc.Namespace, cc.Name, err))
				remaining = append(remaining, cc)
				continue
			}
		}

		// check if other finalizers are present, otherwise the ClusterConfig can be considered fully deleted
		if len(cc.GetFinalizers()) == 0 {
			continue
		}
		clog.Debug("ClusterConfig still has foreign finalizers remaining", "finalizers", cc.GetFinalizers())
		remaining = append(remaining, cc)
	}

	if errs != nil {
		return remaining, fmt.Errorf("one or more errors occurred during handling of orphaned ClusterConfigs:\n%w", errs)
	}
	return remaining, nil
}

func objectKeyFromStrings(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func objectKey(obj client.Object) string {
	return objectKeyFromStrings(obj.GetNamespace(), obj.GetName())
}
