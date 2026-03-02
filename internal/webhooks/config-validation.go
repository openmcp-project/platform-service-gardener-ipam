package webhooks

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	goipam "github.com/metal-stack/go-ipam"

	"github.com/openmcp-project/controller-utils/pkg/logging"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
)

const ConfigValidatorName = "ConfigValidator"

type ConfigValidator struct{}

var _ admission.Validator[*ipamv1alpha1.IPAMConfig] = &ConfigValidator{}

func (c *ConfigValidator) SetupWebhookWithManager(ctx context.Context, mgr ctrl.Manager) error {
	cv := &ConfigValidator{}

	return ctrl.NewWebhookManagedBy(mgr, &ipamv1alpha1.IPAMConfig{}).
		WithValidator(cv).
		Complete()
}

// ValidateCreate implements [admission.Validator].
func (c *ConfigValidator) ValidateCreate(ctx context.Context, cfg *ipamv1alpha1.IPAMConfig) (warnings admission.Warnings, err error) {
	log := logging.FromContextOrDiscard(ctx).WithName(ConfigValidatorName)
	log.Debug("Validating creation of config")
	return nil, ValidateConfig(cfg)
}

// ValidateDelete implements [admission.Validator].
func (c *ConfigValidator) ValidateDelete(ctx context.Context, cfg *ipamv1alpha1.IPAMConfig) (warnings admission.Warnings, err error) {
	log := logging.FromContextOrDiscard(ctx).WithName(ConfigValidatorName)
	log.Debug("Nothing to validate on delete")
	return nil, nil
}

// ValidateUpdate implements [admission.Validator].
func (c *ConfigValidator) ValidateUpdate(ctx context.Context, _ *ipamv1alpha1.IPAMConfig, newCfg *ipamv1alpha1.IPAMConfig) (warnings admission.Warnings, err error) {
	log := logging.FromContextOrDiscard(ctx).WithName(ConfigValidatorName)
	log.Debug("Validating update of config")
	return nil, ValidateConfig(newCfg)
}

func ValidateConfig(cfg *ipamv1alpha1.IPAMConfig) error {
	if cfg == nil {
		return nil
	}
	return validateSpec(&cfg.Spec, field.NewPath("spec")).ToAggregate()
}

func validateSpec(spec *ipamv1alpha1.IPAMConfigSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	parentIDsToSizes := map[string]int{}
	for id, cidrs := range spec.ParentCIDRs {
		// determine smallest CIDR size for each parent ID
		// this means the biggest bitmask value
		smallest := 0
		for i, cidr := range cidrs {
			split := strings.Split(string(cidr), "/")
			if len(split) != 2 {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("parentCIDRs").Key(id).Index(i), cidr, "invalid CIDR format"))
			}
			size, err := strconv.Atoi(split[1])
			if err != nil {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("parentCIDRs").Key(id).Index(i), cidr, "CIDR size cannot be parsed as integer"))
			}
			if size > smallest {
				smallest = size
			}
		}
		parentIDsToSizes[id] = smallest
	}
	allErrs = append(allErrs, validateParentCIDRs(spec.ParentCIDRs, fldPath.Child("parentCIDRs"))...)
	allErrs = append(allErrs, validateInjectionRules(spec.InjectionRules, parentIDsToSizes, fldPath.Child("injectionRules"))...)

	return allErrs
}

func validateParentCIDRs(parentCIDRs map[string][]ipamv1alpha1.CIDR, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// having the same parent CIDR multiple times is not a problem in theory,
	// but there is rarely a reason to do so
	// and having a different but overlapping parent CIDR is actually a problem
	// so just verify that all parent CIDRs are disjunct
	seenCIDRs := make(map[string][]string) // parent CIDR -> ids for which the parent CIDR is defined
	for id, cidrs := range parentCIDRs {
		for i, cidr := range cidrs {
			cidrStr := string(cidr)
			for otherCIDR, otherIDs := range seenCIDRs {
				if err := goipam.PrefixesOverlapping([]string{otherCIDR}, []string{cidrStr}); err != nil {
					allErrs = append(allErrs, field.Invalid(fldPath.Child(id).Index(i), cidrStr, fmt.Sprintf("parent cidr '%s' overlaps with parent cidr '%s' from ids '%v'", cidrStr, otherCIDR, otherIDs)))
				}
			}
			seenCIDRs[cidrStr] = append(seenCIDRs[cidrStr], id)
		}
	}

	return allErrs
}

func validateInjectionRules(rules []ipamv1alpha1.CIDRInjection, parentIDsToSizes map[string]int, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	knownIDs := sets.New[string]()
	for i, rule := range rules {
		rpath := fldPath.Index(i).Key(rule.ID)

		if knownIDs.Has(rule.ID) {
			allErrs = append(allErrs, field.Duplicate(rpath.Child("id"), rule.ID))
		}
		knownIDs.Insert(rule.ID)
		allErrs = append(allErrs, validateInjections(rule.Injections, parentIDsToSizes, rpath.Child("injections"))...)
	}

	return allErrs
}

func validateInjections(injections []ipamv1alpha1.SingleCIDRInjection, parentIDsToSizes map[string]int, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	tmpIDsToSizes := map[string]int{}
	pathsToIndices := map[string]int{} // to detect duplicate paths
	for i, injection := range injections {
		ipath := fldPath.Index(i)
		if injection.ID != "" {
			ipath = ipath.Key(injection.ID)
			if _, exists := tmpIDsToSizes[injection.ID]; exists {
				allErrs = append(allErrs, field.Duplicate(ipath.Child("id"), injection.ID))
			}
			tmpIDsToSizes[injection.ID] = injection.SubnetSize
			if _, exists := parentIDsToSizes[injection.ID]; exists {
				allErrs = append(allErrs, field.Invalid(ipath.Child("id"), injection.ID, "injection ID cannot be the same as a parent CIDRs ID"))
			}
		}

		// note that the same path can be specified with different notations, which is not detected here
		if existingIndex, exists := pathsToIndices[injection.Path]; exists {
			allErrs = append(allErrs, field.Invalid(ipath.Child("path"), injection.Path, fmt.Sprintf("injection injects into the same path as injection at index %d", existingIndex)))
		} else {
			pathsToIndices[injection.Path] = i
		}

		for j, parent := range injection.Parents {
			ppath := ipath.Child("parents").Index(j)
			smallestParentSize, parentExists := parentIDsToSizes[parent]
			tmpSize, tmpExists := tmpIDsToSizes[parent]
			if !parentExists && !tmpExists {
				allErrs = append(allErrs, field.Invalid(ppath, parent, "parent ID is neither in the global list of parent CIDRs, nor the id of a previous injection"))
			} else {
				if tmpExists && injection.SubnetSize <= tmpSize {
					allErrs = append(allErrs, field.Invalid(ppath, parent, fmt.Sprintf("injection lists '%s' with a subnet size of %d as a possible parent, but requests a subnet of size %d itself, which is not smaller than the parent", parent, tmpSize, injection.SubnetSize)))
				}
				if parentExists && injection.SubnetSize <= smallestParentSize {
					allErrs = append(allErrs, field.Invalid(ppath, parent, fmt.Sprintf("injection lists '%s' as a possible parent, but among the possible parent CIDRs is at least one with subnet size %d, which is not bigger than the subnet size %d requested by the injection", parent, smallestParentSize, injection.SubnetSize)))
				}
			}
		}
	}

	return allErrs
}
