package shared_test

import (
	"fmt"
	"maps"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gardenv1alpha1 "github.com/openmcp-project/cluster-provider-gardener/api/core/v1alpha1"
	"github.com/openmcp-project/controller-utils/pkg/collections"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

var _ = Describe("Shared Restore Functions", Serial, func() {

	Context("CheckClusterConfigsForCluster", func() {

		It("should correctly identify ClusterConfigs that need to be created or updated", func() {
			env, _ := defaultTestSetup("testdata", "test-02")
			cluster := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Namespace: "test", Name: "cluster-0"}, cluster)).To(Succeed())

			// compute the expected annotation
			// This is easier than putting it into the Cluster resource hardcoded
			// ruleID -> path -> CIDR
			ann := ipamv1alpha1.AppliedRulesAnnotation{
				"rule1": {
					"/spec/networking/pods":  "10.0.0.0/24",
					"/spec/networking/nodes": "10.0.1.0/24",
				},
				"rule2": {
					"/spec/networking/services": "12.0.0.0/24",
				},
				"rule3": {
					"/spec/networking/whatever": "12.0.1.0/24",
				},
				"rule4": {
					"/spec/networking/cidr": "12.0.2.0/24",
				},
			}

			ir, err := shared.CheckClusterConfigsForCluster(env.Ctx, env.Client(), cluster, ann, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(ir).ToNot(BeNil())

			expectedClusterConfigs := map[string]*gardenv1alpha1.ClusterConfig{}
			for ruleID, pathToCIDR := range ann {
				ecc, err := shared.GenerateClusterConfigForInjectionList(cluster, ruleID, pathToCIDR)
				Expect(err).ToNot(HaveOccurred())
				expectedClusterConfigs[ruleID] = ecc
			}

			Expect(ir.ClusterConfigsToCreate).To(ConsistOf(
				expectedClusterConfigs["rule4"], // the ClusterConfig for rule4 is missing
			))
			Expect(ir.ClusterConfigsToUpdate).To(beEqualByNameLabelsSpec(
				compareFinalizers, compareLabels, compareSpec,
				expectedClusterConfigs["rule1"], // the ClusterConfig for rule1 exists, but one patch statement is missing
				expectedClusterConfigs["rule3"], // the ClusterConfig for rule3 exists, but is missing the finalizer
			))
		})

	})

	Context("RestoreIPAMFromClusterState", func() {

		parent10 := "10.0.0.0/16"
		parent12 := "12.0.0.0/16"
		child10024 := "10.0.0.0/24"
		child10124 := "10.0.1.0/24"
		child12024 := "12.0.0.0/24"
		child12124 := "12.0.1.0/24"
		child12224 := "12.0.2.0/24"

		It("should restore the IPAM state from a simple cluster state", func() {
			env, _ := defaultTestSetup("testdata", "test-01")
			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())

			// get all CIDRs
			cidrStrings, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrStrings).To(ConsistOf(parent10, parent12, child10024, child10124, child12024, child12224, "13.0.3.0/24"))

			for _, cs := range cidrStrings {
				cidr, err := shared.IPAM.PrefixFrom(env.Ctx, cs)
				Expect(err).ToNot(HaveOccurred())
				if cs != parent10 && strings.HasPrefix(cs, "10.0") {
					Expect(cidr.ParentCidr).To(Equal(parent10))
				} else if cs != parent12 && strings.HasPrefix(cs, "12.0") {
					Expect(cidr.ParentCidr).To(Equal(parent12))
				} else {
					Expect(cidr.ParentCidr).To(BeEmpty())
				}
			}
		})

		It("should restore missing ClusterConfigs from the annotation", func() {
			env, _ := defaultTestSetup("testdata", "test-02")

			ann := ipamv1alpha1.AppliedRulesAnnotation{
				"rule1": {
					"/spec/networking/pods":  child10024,
					"/spec/networking/nodes": child10124,
				},
				"rule2": {
					"/spec/networking/services": child12024,
				},
				"rule3": {
					"/spec/networking/whatever": child12124,
				},
				"rule4": {
					"/spec/networking/cidr": child12224,
				},
			}
			annString, err := ann.ToAnnotation()
			Expect(err).ToNot(HaveOccurred())

			cluster := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Namespace: "test", Name: "cluster-0"}, cluster)).To(Succeed())
			cluster.Annotations = map[string]string{}
			cluster.Annotations[ipamv1alpha1.AppliedRulesAnnotationKey] = annString
			Expect(env.Client().Update(env.Ctx, cluster)).To(Succeed())
			oldCCs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(oldCCs).To(HaveLen(3))

			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())
			newCCs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(newCCs).To(HaveLen(4)) // one new ClusterConfig should have been created

			// get all CIDRs
			cidrStrings, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrStrings).To(ConsistOf(parent10, parent12, child10024, child10124, child12024, child12124, child12224))

			for _, cs := range cidrStrings {
				cidr, err := shared.IPAM.PrefixFrom(env.Ctx, cs)
				Expect(err).ToNot(HaveOccurred())
				if cs != parent10 && strings.HasPrefix(cs, "10.0") {
					Expect(cidr.ParentCidr).To(Equal(parent10))
				} else if cs != parent12 && strings.HasPrefix(cs, "12.0") {
					Expect(cidr.ParentCidr).To(Equal(parent12))
				} else {
					Expect(cidr.ParentCidr).To(BeEmpty())
				}
			}
		})

		It("should correctly restore nested CIDRs", func() {
			env, _ := defaultTestSetup("testdata", "test-03")
			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())

			child10028 := "10.0.0.0/28"
			child10030 := "10.0.0.0/30"
			child1001628 := "10.0.0.16/28"
			child1001630 := "10.0.0.16/30"

			// get all CIDRs
			cidrStrings, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrStrings).To(ConsistOf(parent10, child10024, child10028, child10030, child1001628, child1001630))

			for child, parent := range map[string]string{
				parent10:     "",
				child10024:   parent10,
				child10028:   child10024,
				child10030:   child10028,
				child1001628: child10024,
				child1001630: child1001628,
			} {
				cidr, err := shared.IPAM.PrefixFrom(env.Ctx, child)
				Expect(err).ToNot(HaveOccurred())
				Expect(cidr.ParentCidr).To(Equal(parent))
			}
		})

	})

	Context("HandleOrphanedClusterConfigs", func() {

		It("should not do anything if there are no orphaned ClusterConfigs", func() {
			env, _ := defaultTestSetup("testdata", "test-01")
			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())
			oldCCs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(oldCCs).To(HaveLen(5))
			oldCidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())

			remainingCCs, err := shared.HandleOrphanedClusterConfigs(env.Ctx, env.Client(), oldCCs...)
			Expect(err).ToNot(HaveOccurred())
			Expect(remainingCCs).To(HaveLen(len(oldCCs)))
			cidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrs).To(ConsistOf(oldCidrs))
			ccs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(ccs).To(beEqualByNameLabelsSpec(oldCCs, compareLabels, compareSpec))
		})

		It("should delete orphaned ClusterConfigs without releasing CIDRs if there is no CIDR management finalizer", func() {
			env, _ := defaultTestSetup("testdata", "test-01")
			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())

			// remove cluster-0, which should allow two ClusterConfigs to be cleaned up
			cl := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Namespace: "test", Name: "cluster-0"}, cl)).To(Succeed())
			Expect(env.Client().Delete(env.Ctx, cl)).To(Succeed())

			// remove the CIDR management finalizer from the ClusterConfigs belonging to cluster-0
			c0ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, env.Client(), cl)
			Expect(err).ToNot(HaveOccurred())
			for _, cc := range c0ccs {
				old := cc.DeepCopy()
				Expect(controllerutil.RemoveFinalizer(cc, ipamv1alpha1.CIDRManagementFinalizer)).To(BeTrue())
				Expect(env.Client().Patch(env.Ctx, cc, client.MergeFrom(old))).To(Succeed())
			}

			oldCCs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(oldCCs).To(HaveLen(5))
			oldCidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())

			remainingCCs, err := shared.HandleOrphanedClusterConfigs(env.Ctx, env.Client(), oldCCs...)
			Expect(err).ToNot(HaveOccurred())
			Expect(remainingCCs).To(HaveLen(len(oldCCs) - 2))
			cidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrs).To(ConsistOf(oldCidrs))
			ccs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(ccs).To(HaveLen(len(oldCCs) - 2))
			for _, cc := range ccs {
				Expect(cc.Name).ToNot(ContainSubstring("cluster-0"))
			}
		})

		It("should delete orphaned ClusterConfigs and release the corresponding CIDRs if there is a CIDR management finalizer", func() {
			env, _ := defaultTestSetup("testdata", "test-01")
			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())
			oldCCs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(oldCCs).To(HaveLen(5))
			oldCidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())

			// remove cluster-0, which should allow two ClusterConfigs to be cleaned up
			cl := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Namespace: "test", Name: "cluster-0"}, cl)).To(Succeed())
			Expect(env.Client().Delete(env.Ctx, cl)).To(Succeed())

			remainingCCs, err := shared.HandleOrphanedClusterConfigs(env.Ctx, env.Client(), oldCCs...)
			Expect(err).ToNot(HaveOccurred())
			Expect(remainingCCs).To(HaveLen(len(oldCCs) - 2))
			cidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrs).To(HaveLen(len(oldCidrs) - 2))
			Expect(cidrs).ToNot(ContainElements("10.0.0.0/24", "12.0.0.0/24"))
			ccs, err := shared.FetchClusterConfigs(env.Ctx, env.Client())
			Expect(err).ToNot(HaveOccurred())
			Expect(ccs).To(HaveLen(len(oldCCs) - 2))
			for _, cc := range ccs {
				Expect(cc.Name).ToNot(ContainSubstring("cluster-0"))
			}
		})

	})

})

// beEqualByNameLabelsSpec is a custom Gomega matcher to compare lists of ClusterConfigs.
// It can take multiple ClusterConfigs and/or slices of ClusterConfigs.
// For each field that should be compared in addition to name and namespace, the corresponding comparator (compareLabels, compareSpec, compareFinalizers) must be passed in as an argument.
func beEqualByNameLabelsSpec(expected ...any) types.GomegaMatcher {
	objs := []*gardenv1alpha1.ClusterConfig{}
	res := &beEqualByNameLabelsSpecMatcher{
		comparators: sets.New[clusterConfigFieldComparator](),
	}
	// each element is expected to be a *gardenv1alpha1.ClusterConfig, a slice of those, or a comparator
	for _, e := range expected {
		switch v := e.(type) {
		case clusterConfigFieldComparator:
			res.comparators.Insert(v)
		case []*gardenv1alpha1.ClusterConfig:
			for _, obj := range v {
				ExpectWithOffset(1, obj).ToNot(BeNil(), "nil object passed into beEqualByNameLabelsSpec")
				objs = append(objs, obj)
			}
		case *gardenv1alpha1.ClusterConfig:
			ExpectWithOffset(1, v).ToNot(BeNil(), "nil object passed into beEqualByNameLabelsSpec")
			objs = append(objs, v)
		default:
			ExpectWithOffset(1, e).To(BeAssignableToTypeOf(&gardenv1alpha1.ClusterConfig{}), "each argument passed into beEqualByNameLabelsSpec must be either a *gardenv1alpha1.ClusterConfig, a slice of those, or a clusterConfigFieldComparator")
			ExpectWithOffset(1, e).ToNot(BeNil(), "nil object passed into beEqualByNameLabelsSpec")
		}

	}
	res.expected = collections.ProjectSliceToMap(objs, func(obj *gardenv1alpha1.ClusterConfig) (string, *gardenv1alpha1.ClusterConfig) {
		return fmt.Sprintf("%s/%s", obj.GetNamespace(), obj.GetName()), obj
	})
	return res
}

type beEqualByNameLabelsSpecMatcher struct {
	expected    map[string]*gardenv1alpha1.ClusterConfig
	comparators sets.Set[clusterConfigFieldComparator]
}

type clusterConfigFieldComparator string

const (
	compareLabels     clusterConfigFieldComparator = "labels"
	compareSpec       clusterConfigFieldComparator = "spec"
	compareFinalizers clusterConfigFieldComparator = "finalizers"
)

// Match implements [types.GomegaMatcher].
func (b *beEqualByNameLabelsSpecMatcher) Match(actual any) (success bool, err error) {
	var actualObjs []*gardenv1alpha1.ClusterConfig
	switch a := actual.(type) {
	case []*gardenv1alpha1.ClusterConfig:
		actualObjs = a
	case *gardenv1alpha1.ClusterConfig:
		actualObjs = []*gardenv1alpha1.ClusterConfig{a}
	default:
		return false, fmt.Errorf("actual value must be either a *gardenv1alpha1.ClusterConfig or a slice of *gardenv1alpha1.ClusterConfig")
	}

	ExpectWithOffset(1, actualObjs).To(HaveLen(len(b.expected)), "number of actual objects does not match number of expected objects")
	expectedObjs := make(map[string]*gardenv1alpha1.ClusterConfig, len(b.expected))
	maps.Copy(expectedObjs, b.expected)

	for _, aObj := range actualObjs {
		key := fmt.Sprintf("%s/%s", aObj.GetNamespace(), aObj.GetName())
		eObj, ok := expectedObjs[key]
		ExpectWithOffset(1, ok).To(BeTrue(), "actual contains object '%s' which is not expected", key)
		if b.comparators.Has(compareLabels) {
			ExpectWithOffset(1, maps.Equal(eObj.Labels, aObj.Labels)).To(BeTrue(), "labels do not match for object '%s'", key)
		}
		if b.comparators.Has(compareFinalizers) {
			ExpectWithOffset(1, aObj.Finalizers).To(ContainElements(eObj.Finalizers))
			ExpectWithOffset(1, eObj.Finalizers).To(ContainElements(aObj.Finalizers))
		}
		if b.comparators.Has(compareSpec) {
			ExpectWithOffset(1, eObj.Spec).To(Equal(aObj.Spec), "spec does not match for object '%s'", key)
		}
	}

	return true, nil
}

// FailureMessage implements [types.GomegaMatcher].
func (b *beEqualByNameLabelsSpecMatcher) FailureMessage(actual any) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nto be identical (comparing %s) to\n\t%#v", actual, printComparators(b.comparators), b.expected)
}

// NegatedFailureMessage implements [types.GomegaMatcher].
func (b *beEqualByNameLabelsSpecMatcher) NegatedFailureMessage(actual any) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nto not be identical (comparing %s) to\n\t%#v", actual, printComparators(b.comparators), b.expected)
}

func printComparators(comps sets.Set[clusterConfigFieldComparator]) string {
	return strings.Join(collections.ProjectSliceToSlice(sets.List(comps), func(c clusterConfigFieldComparator) string {
		return string(c)
	}), ", ")
}
