package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	goipam "github.com/metal-stack/go-ipam"

	gardenv1alpha1 "github.com/openmcp-project/cluster-provider-gardener/api/core/v1alpha1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/controller-utils/pkg/collections"
	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	"github.com/openmcp-project/controller-utils/pkg/jsonpatch"
	"github.com/openmcp-project/controller-utils/pkg/logging"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	openmcpconst "github.com/openmcp-project/openmcp-operator/api/constants"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

const (
	ControllerName = "IPAMCluster"

	EventReasonReconcileSuccess    = "ReconcileSuccess"
	EventReasonReconcileError      = "ReconcileError"
	EventReasonCIDRManagementError = "CIDRManagementError"

	EventActionFetchingCluster                  = "FetchingCluster"
	EventActionHandlingOperationAnnotation      = "HandlingOperationAnnotation"
	EventActionParsingAppliedRules              = "ParsingAppliedRules"
	EventActionEvaluatingExistingClusterConfigs = "EvaluatingExistingClusterConfigs"
	EventActionEvaluatingMissingInjections      = "EvaluatingMissingInjections"
	EventActionEvaluatingRulesToApply           = "EvaluatingRulesToApply"
	EventActionCIDRGeneration                   = "CIDRGeneration"
	EventActionApplyingChanges                  = "ApplyingChanges"
	EventActionReleasingCIDRs                   = "ReleasingCIDRs"
)

type IPAMClusterController struct {
	PlatformCluster *clusters.Cluster
	er              events.EventRecorder
}

var _ reconcile.Reconciler = &IPAMClusterController{}

func NewIPAMClusterController(platformCluster *clusters.Cluster, er events.EventRecorder) *IPAMClusterController {
	return &IPAMClusterController{
		PlatformCluster: platformCluster,
		er:              er,
	}
}

type ReconcileResult struct {
	Cluster       *clustersv1alpha1.Cluster
	ClusterConfig *gardenv1alpha1.ClusterConfig
	EventAction   string
	Result        reconcile.Result
	Error         error
}

func (rr *ReconcileResult) defaultedEventAction() string {
	if rr.EventAction != "" {
		return rr.EventAction
	}
	return "Reconcile"
}

func (c *IPAMClusterController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := logging.FromContextOrPanic(ctx).WithName(ControllerName)
	ctx = logging.NewContext(ctx, log)
	log.Info("Starting reconcile")

	rr := c.reconcile(ctx, req)
	if c.er != nil {
		if rr.Error != nil {
			if rr.Cluster != nil {
				c.er.Eventf(rr.Cluster, rr.ClusterConfig, corev1.EventTypeWarning, "ReconcileError", rr.defaultedEventAction(), "Reconcile failed: %v", rr.Error)
			}
		} else {
			if rr.Cluster != nil {
				c.er.Eventf(rr.Cluster, rr.ClusterConfig, corev1.EventTypeNormal, "ReconcileSuccess", rr.defaultedEventAction(), "Reconcile successful")
			}
		}
	}

	return rr.Result, rr.Error
}

func (c *IPAMClusterController) reconcile(ctx context.Context, req reconcile.Request) ReconcileResult {
	log := logging.FromContextOrPanic(ctx)

	// fetch cluster
	cl := &clustersv1alpha1.Cluster{}
	if err := c.PlatformCluster.Client().Get(ctx, req.NamespacedName, cl); err != nil {
		if apierrors.IsNotFound(err) {
			return ReconcileResult{
				EventAction: EventActionReleasingCIDRs,
				Error:       c.handleDelete(ctx, req),
			}
		}
		return ReconcileResult{EventAction: EventActionFetchingCluster, Error: fmt.Errorf("error fetching Cluster %s: %w", req.String(), err)}
	}

	// handle operation annotation
	if cl.GetAnnotations() != nil {
		op, ok := cl.GetAnnotations()[openmcpconst.OperationAnnotation]
		if ok {
			switch op {
			case openmcpconst.OperationAnnotationValueIgnore:
				log.Info("Ignoring resource due to ignore operation annotation")
				return ReconcileResult{}
			case openmcpconst.OperationAnnotationValueReconcile:
				log.Debug("Removing reconcile operation annotation from resource")
				if err := ctrlutils.EnsureAnnotation(ctx, c.PlatformCluster.Client(), cl, openmcpconst.OperationAnnotation, "", true, ctrlutils.DELETE); err != nil {
					return ReconcileResult{EventAction: EventActionHandlingOperationAnnotation, Error: fmt.Errorf("error removing operation annotation: %w", err)}
				}
			}
		}
	}

	// figure out which CIDR injection rules from the config apply to this cluster
	cfg := shared.GetConfig()
	if cfg == nil {
		log.Info("No GardenerIPAMConfig known, skipping reconciliation")
		return ReconcileResult{}
	}
	applicableRules := []ipamv1alpha1.CIDRInjection{}
	for _, rule := range cfg.Spec.InjectionRules {
		if rule.Matches(cl) {
			applicableRules = append(applicableRules, rule)
		}
	}
	if len(applicableRules) == 0 {
		log.Info("No applicable CIDR injection rules for this cluster, skipping reconciliation")
		return ReconcileResult{}
	}
	log.Info("Found applicable CIDR injection rules", "ids", collections.ProjectSliceToSlice(applicableRules, func(rule ipamv1alpha1.CIDRInjection) string {
		return rule.ID
	}))

	rr := ReconcileResult{
		Cluster: cl,
	}

	// It sounds a bit unintuitive to do this also when the Cluster already has a deletion timestamp,
	// but we want to make sure the CIDRs are blocked and the Cluster is up-to-date, as long as the Cluster still exists.
	// Some deletion logic will happen once the Cluster resource has actually been deleted.
	rr = c.handleCreateOrUpdate(ctx, rr, applicableRules)

	return rr
}

func (c *IPAMClusterController) handleCreateOrUpdate(ctx context.Context, rr ReconcileResult, applicableRules []ipamv1alpha1.CIDRInjection) ReconcileResult {
	log := logging.FromContextOrPanic(ctx)

	// fetch the config
	cfg := shared.GetConfig()

	// fetch all ClusterConfigs managed by this controller for this Cluster
	ccs, err := shared.FetchClusterConfigsForCluster(ctx, c.PlatformCluster, rr.Cluster)
	if err != nil {
		return ReconcileResult{Cluster: rr.Cluster, EventAction: EventActionEvaluatingExistingClusterConfigs, Error: fmt.Errorf("error fetching ClusterConfigs for Cluster: %w", err)}
	}

	// manage CIDR injections
	log.Debug("Managing CIDR injections")
	ir, eventAction, err := c.ManageInjections(ctx, rr.Cluster, cfg, applicableRules, ccs)
	if err != nil {
		c.er.Eventf(rr.Cluster, nil, corev1.EventTypeWarning, EventReasonCIDRManagementError, eventAction, "an error occurred during CIDR injection management: %s", err.Error())
	}
	errs := err

	// if the result is not nil, apply it, even if an error occurred, because the internal CIDR management state has changed according to it
	if ir != nil {
		// apply annotation
		if len(ir.AnnotationToApply) > 0 {
			annotationValue, err := ir.AnnotationToApply.ToAnnotation()
			if err != nil {
				errs = errors.Join(errs, fmt.Errorf("unable to serialize applied rules annotation: %w", err))
				c.er.Eventf(rr.Cluster, nil, corev1.EventTypeWarning, EventReasonCIDRManagementError, EventActionApplyingChanges, "could not convert CIDR annotation value into JSON: %s", err.Error())
			}
			log.Debug("Applying annotation to Cluster", "value", annotationValue)
			old := rr.Cluster.DeepCopy()
			if rr.Cluster.Annotations == nil {
				rr.Cluster.Annotations = map[string]string{}
			}
			rr.Cluster.Annotations[ipamv1alpha1.AppliedRulesAnnotationKey] = annotationValue
			if err := c.PlatformCluster.Client().Patch(ctx, rr.Cluster, client.MergeFrom(old)); err != nil {
				errs = errors.Join(errs, fmt.Errorf("unable to update Cluster with applied rules annotation: %w", err))
				c.er.Eventf(rr.Cluster, nil, corev1.EventTypeWarning, EventReasonCIDRManagementError, EventActionApplyingChanges, "could not update Cluster with applied rules annotation: %s", err.Error())
			}
		}

		// create/update ClusterConfigs
		countCreate, countUpdate := ir.Size()
		log.Debug("Creating/Updating ClusterConfigs", "countCreate", countCreate, "countUpdate", countUpdate)
		if err := ir.Apply(ctx, c.PlatformCluster); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	// log rules which were skipped due to issues
	for ruleID, issue := range ir.RulesWithIssues {
		errs = errors.Join(errs, fmt.Errorf("problem with injection rule '%s': %w", ruleID, issue))
		c.er.Eventf(rr.Cluster, nil, corev1.EventTypeWarning, EventReasonCIDRManagementError, EventActionEvaluatingRulesToApply, "skipped injection rule '%s' due to issue: %s", ruleID, issue.Error())
	}

	// log rules which were skipped because they had already been applied before
	for ruleID := range ir.SkippedBecauseAlreadyAppliedRules {
		log.Debug("Skipped rule which had already been applied before", "id", ruleID)
		c.er.Eventf(rr.Cluster, nil, corev1.EventTypeNormal, EventReasonCIDRManagementError, EventActionEvaluatingRulesToApply, "skipped injection rule '%s' because it had already been applied before", ruleID)
	}

	rr.Error = errs
	return rr
}

func (c *IPAMClusterController) handleDelete(ctx context.Context, req reconcile.Request) error {
	log := logging.FromContextOrPanic(ctx)
	log.Info("Cluster does not exist anymore, freeing CIDRs of remaining ClusterConfig resources")

	// This method is executed after the Cluster resource has been deleted.
	// We need to identify all ClusterConfigs that belonged to this Cluster, free the corresponding CIDRs and remove the finalizers from the ClusterConfigs.
	ccs := &gardenv1alpha1.ClusterConfigList{}
	if err := c.PlatformCluster.Client().List(ctx, ccs, client.MatchingLabels(shared.ClusterConfigLabels(&clustersv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace}}, "")), client.InNamespace(req.Namespace)); err != nil {
		return fmt.Errorf("unable to list ClusterConfig resources: %w", err)
	}
	log.Debug("Fetched ClusterConfig resources that belonged to the deleted Cluster", "count", len(ccs.Items))

	var errs error
	for _, cc := range ccs.Items {
		if err := shared.ReleaseCIDRsForClusterConfig(ctx, c.PlatformCluster, &cc); err != nil {
			errs = errors.Join(errs, fmt.Errorf("error trying to release CIDRs from ClusterConfig '%s/%s': %w", cc.Namespace, cc.Name, err))
		}
	}

	return errs
}

type InjectionManagementResult struct {
	*shared.RestorationInstruction
	// AnnotationToApply is the updated AppliedRulesAnnotation that should be applied to the Cluster resource.
	AnnotationToApply ipamv1alpha1.AppliedRulesAnnotation
	// RulesWithIssues contains any rules that were skipped due to issues (usually conflicting injection paths).
	RulesWithIssues map[string]error
	// SkippedBecauseAlreadyAppliedRules contains the IDs of rules that were skipped because they had already been applied before.
	SkippedBecauseAlreadyAppliedRules sets.Set[string]
}

// ManageInjections contains the core logic for determining which CIDRs need to be injected into a Cluster.
// Among context and Cluster, it takes the following parameters:
// - applicableRules: The list of CIDRInjection rules that apply to the Cluster
// - ccs: The list of existing ClusterConfig resources managed by this controller for this Cluster
// It returns the updated AppliedRulesAnnotation as well as the list of ClusterConfig resources to create or update.
// None of the passed-in objects is modified, nor does any interaction with the platform cluster happen here.
// RulesWithIssues will contain one entry for each rule in applicableRules that would inject a CIDR in a path where another rule has already injected a CIDR into. Only rules which would result in new ClusterConfigs are considered here, conflicting updates for existing ClusterConfigs are simply ignored.
// This function may still return a non-nil InjectionManagementResult, even if an error occurred. In this case, the returned result should be applied, because the internal CIDR management state has changed according to it.
func (c *IPAMClusterController) ManageInjections(ctx context.Context, cl *clustersv1alpha1.Cluster, cfg *ipamv1alpha1.IPAMConfig, applicableRules []ipamv1alpha1.CIDRInjection, ccs map[string]*gardenv1alpha1.ClusterConfig) (*InjectionManagementResult, string, error) {
	log := logging.FromContextOrPanic(ctx)
	allErrs := []error{}

	// first, parse the applied rules annotation to see what has already been injected
	log.Debug("Parsing applied rules annotation")
	appliedRulesString, ok := cl.Annotations[ipamv1alpha1.AppliedRulesAnnotationKey]
	appliedRules := ipamv1alpha1.AppliedRulesAnnotation{}
	if ok {
		if err := appliedRules.FromAnnotation(appliedRulesString); err != nil {
			return nil, EventActionParsingAppliedRules, fmt.Errorf("unable to recover applied rules: %w", err)
		}
	}
	log.Debug("Successfully parsed applied rules annotation", "annotation", appliedRulesString)

	// add entries for existing ClusterConfigs to the appliedRules map, if missing
	log.Debug("Evaluating existing ClusterConfigs")
	for ruleID, cc := range ccs {
		injectedCIDRsForRule, ok := appliedRules[ruleID]
		if !ok {
			injectedCIDRsForRule = map[string]string{}
			appliedRules[ruleID] = injectedCIDRsForRule
		}
		for _, patch := range cc.Spec.Patches {
			// update appliedRules map if this path is not yet recorded
			// do not update if the path exists, even if the value differs!
			if _, ok := injectedCIDRsForRule[patch.Path]; !ok {
				pval := ""
				if err := json.Unmarshal(patch.Value.Raw, &pval); err != nil {
					allErrs = append(allErrs, fmt.Errorf("error unmarshaling value for path '%s' from ClusterConfig '%s/%s': %w", patch.Path, cc.Namespace, cc.Name, err))
					continue
				}
				injectedCIDRsForRule[patch.Path] = pval
			}
		}
	}
	if err := errors.Join(allErrs...); err != nil {
		return nil, EventActionEvaluatingExistingClusterConfigs, fmt.Errorf("errors occurred while evaluating existing ClusterConfigs:\n%w", err)
	}

	// figure out all paths into which a CIDR has already been injected
	injectedPaths := collections.AggregateMap(appliedRules, func(k string, v map[string]string, aggregated sets.Set[string]) sets.Set[string] {
		return aggregated.Insert(sets.KeySet(v).UnsortedList()...)
	}, sets.New[string]())
	log.Debug("Evaluated already injected paths", "paths", sets.List(injectedPaths))

	res := &InjectionManagementResult{
		AnnotationToApply:                 appliedRules.DeepCopy(),
		RulesWithIssues:                   map[string]error{},
		SkippedBecauseAlreadyAppliedRules: sets.New[string](),
	}

	var err error
	res.RestorationInstruction, err = shared.CheckClusterConfigsForCluster(ctx, c.PlatformCluster, cl, appliedRules, ccs)
	if err != nil {
		return nil, EventActionEvaluatingMissingInjections, fmt.Errorf("errors occurred while evaluating missing injections:\n%w", err)
	}

	// evaluate applicableRules
	// - rules which generate new ruleIDs for paths which have not been used by another injection rule before are fine and will result in new ClusterConfigs
	// - rules which generate existing ruleIDs will be ignored, as we consider rules and individual injections immutable
	// - rules which generate new ruleIDs but try to inject into paths which have already been used by another injection rule will be recorded in RulesWithIssues
	for _, rule := range applicableRules {
		if _, ok := appliedRules[rule.ID]; ok {
			// ruleID already exists, skip
			log.Debug("Skipping CIDR injection rule as a rule with this ID has been applied before", "id", rule.ID)
			res.SkippedBecauseAlreadyAppliedRules.Insert(rule.ID)
			continue
		}

		thisRulesPaths := sets.New[string]()
		for i := range rule.Injections {
			injection := &rule.Injections[i]
			// normalize path for comparison
			path, err := jsonpatch.ConvertPath(injection.Path)
			if err != nil {
				allErrs = append(allErrs, fmt.Errorf("error converting path '%s' from injection at index %d of rule '%s': %w", injection.Path, i, rule.ID, err))
				continue
			}
			injection.Path = path

			if thisRulesPaths.Has(path) {
				res.RulesWithIssues[rule.ID] = fmt.Errorf("rule '%s' contains multiple injections for the same path '%s'", rule.ID, path)
				break
			}
			thisRulesPaths.Insert(path)
			if injectedPaths.Has(path) {
				// path already used by another rule
				res.RulesWithIssues[rule.ID] = fmt.Errorf("rule '%s' tries to inject into path '%s' which has already been used by another injection rule", rule.ID, path)
				break
			}
		}

		if _, hasIssue := res.RulesWithIssues[rule.ID]; hasIssue {
			log.Debug("Skipping CIDR injection rule due to issues", "id", rule.ID, "issue", res.RulesWithIssues[rule.ID])
			continue
		}

		// generate CIDRs for this rule's injections
		injections, err := generateCIDRsForInjectionRule(ctx, &rule, cfg.Spec.ParentCIDRs)
		if err != nil {
			allErrs = append(allErrs, fmt.Errorf("error generating CIDRs for injection rule '%s': %w", rule.ID, err))
			continue
		}

		newCC, err := shared.GenerateClusterConfigForInjectionList(cl, rule.ID, injections)
		if err != nil {
			allErrs = append(allErrs, fmt.Errorf("error generating ClusterConfig for injection rule '%s': %w", rule.ID, err))
			continue
		}
		res.ClusterConfigsToCreate = append(res.ClusterConfigsToCreate, newCC)
	}

	return res, EventActionCIDRGeneration, errors.Join(allErrs...)
}

// generateCIDRsForInjectionRule tries to generate CIDRs for all injections defined in the given rule.
// It expects the paths in the injections to have been normalized already.
// Note that if no error is returned, the CIDRs have been blocked in the shared IPAM instance and need to be released again when no longer needed.
func generateCIDRsForInjectionRule(ctx context.Context, rule *ipamv1alpha1.CIDRInjection, parentCIDRs map[string][]ipamv1alpha1.CIDR) (map[string]string, error) {
	log := logging.FromContextOrPanic(ctx)

	raw := map[string]*goipam.Prefix{}
	tmpParents := map[string][]*goipam.Prefix{} // will always contain only a single value, but is easier to handle as a slice implementation-wise

	for injIdx, injection := range rule.Injections {
		ilog := log.WithValues("injectionIndex", injIdx, "ruleID", rule.ID)
		selectedParents := injection.Parents
		if len(selectedParents) == 0 {
			// wildcard, select all parent CIDRs
			ilog.Debug("List of allowed parents is empty, using all globally defined parent CIDRs")
			selectedParents = sets.KeySet(parentCIDRs).UnsortedList()
		}
		for _, parentID := range selectedParents {
			plog := ilog.WithValues("parentID", parentID)
			// try to find parent CIDR
			// check tmpParents first
			prefixes, ok := tmpParents[parentID]
			if !ok {
				cidrs, ok := parentCIDRs[parentID]
				if !ok {
					plog.Error(nil, "rule refers to unknown parent CIDR ID")
					continue
				}
				prefixes = []*goipam.Prefix{}
				for cIdx, cidr := range cidrs {
					prefix, err := shared.IPAM.PrefixFrom(ctx, string(cidr))
					if err != nil {
						plog.Error(err, "unable to retrieve parent CIDR from internal storage", "cidr", cidr, "cidrIndex", cIdx)
						continue
					}
					prefixes = append(prefixes, prefix)
				}
			}
			success := false
			for _, prefix := range prefixes {
				// found possible parent prefix, try to acquire child prefix
				plog.Debug("Found possible parent prefix, trying to acquire child prefix", "parentCIDR", prefix.Cidr)
				cPrefix, err := shared.IPAM.AcquireChildPrefix(ctx, prefix.Cidr, uint8(injection.SubnetSize))
				if err != nil {
					plog.Debug("Unable to use parent CIDR", "error", err.Error())
					continue
				}
				plog.Debug("Successfully generated child CIDR", "childCidr", cPrefix.Cidr)
				raw[injection.Path] = cPrefix
				if injection.ID != "" {
					tmpParents[injection.ID] = []*goipam.Prefix{prefix}
				}
				success = true
				break
			}
			if success {
				break
			}
		}
		if _, ok := raw[injection.Path]; !ok {
			// unable to generate CIDR for this injection
			ilog.Debug("Unable to generate CIDR for rule")

			// try to release any already acquired CIDRs for this rule
			for _, cPrefix := range raw {
				if err := shared.IPAM.ReleaseChildPrefix(ctx, cPrefix); err != nil {
					ilog.Error(err, "error releasing child prefix")
				}
			}

			return nil, fmt.Errorf("unable to generate CIDR for injection %d (path '%s') from rule '%s'", injIdx, injection.Path, rule.ID)
		}
	}

	return collections.ProjectMapToMap(raw, func(k string, v *goipam.Prefix) (string, string) {
		return k, v.Cidr
	}), nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *IPAMClusterController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// watch Cluster resources
		For(&clustersv1alpha1.Cluster{}, builder.WithPredicates(predicate.And(
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				cfg := shared.GetConfig()
				if cfg == nil {
					return false
				}
				pobj, ok := obj.(clustersv1alpha1.ObjectWithPurposes)
				if !ok {
					return false
				}
				for _, rule := range cfg.Spec.InjectionRules {
					if rule.Matches(pobj) {
						return true
					}
				}
				return false
			}),
			predicate.Or(
				// This way, we will miss cases where a Cluster is first created and then later changed to match the selector,
				// but we will have significantly less reconciles for Clusters that already match the selector.
				// Since these cases should appear only rarely and can easily be mitigated by adding the reconcile annotation, not reconciling on updates should be fine.
				ctrlutils.OnCreatePredicate(),
				ctrlutils.OnDeletePredicate(),
				ctrlutils.GotAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueReconcile),
				ctrlutils.LostAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueIgnore),
			),
			predicate.Not(
				ctrlutils.HasAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueIgnore),
			),
		))).
		// watch owned ClusterConfig resources with the fitting labels
		Owns(&gardenv1alpha1.ClusterConfig{}, builder.WithPredicates(predicate.And(
			ctrlutils.HasLabelPredicate(openmcpconst.ManagedByLabel, shared.ProviderName()),
			ctrlutils.HasLabelPredicate(openmcpconst.ManagedPurposeLabel, ipamv1alpha1.ManagedPurposeLabelValue),
			ctrlutils.HasLabelPredicate(openmcpconst.EnvironmentLabel, shared.Environment()),
			predicate.Or(
				ctrlutils.OnUpdatePredicate(),
				ctrlutils.OnDeletePredicate(),
				ctrlutils.GotAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueReconcile),
				ctrlutils.LostAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueIgnore),
			),
			predicate.Not(
				ctrlutils.HasAnnotationPredicate(openmcpconst.OperationAnnotation, openmcpconst.OperationAnnotationValueIgnore),
			),
		))).
		// react to manual reconciliation triggers from the config controller
		WatchesRawSource(source.Channel(shared.ClusterWatch, &handler.EnqueueRequestForObject{})).
		Complete(c)
}
