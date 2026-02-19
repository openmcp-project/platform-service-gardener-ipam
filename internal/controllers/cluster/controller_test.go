package cluster_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	goipam "github.com/metal-stack/go-ipam"

	gardenv1alpha1 "github.com/openmcp-project/cluster-provider-gardener/api/core/v1alpha1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	testutils "github.com/openmcp-project/controller-utils/pkg/testing"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/controllers/cluster"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

func defaultTestSetup(testDirPathSegments ...string) (*testutils.Environment, *clusters.Cluster) {
	env := testutils.NewEnvironmentBuilder().
		WithFakeClient(platformScheme).
		WithInitObjectPath(testDirPathSegments...).
		WithReconcilerConstructor(func(c client.Client) reconcile.Reconciler {
			return cluster.NewIPAMClusterController(clusters.NewTestClusterFromClient(platformCluster, c), nil)
		}).
		Build()

	cfg := &ipamv1alpha1.IPAMConfig{}
	Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: providerName}, cfg)).To(Succeed())
	shared.SetConfig(cfg)
	for _, parentList := range cfg.Spec.ParentCIDRs {
		for _, parent := range parentList {
			_, err := shared.IPAM.PrefixFrom(env.Ctx, string(parent))
			if err != nil {
				_, err := shared.IPAM.NewPrefix(env.Ctx, string(parent))
				Expect(err).ToNot(HaveOccurred())
			}
		}
	}

	return env, clusters.NewTestClusterFromClient(platformCluster, env.Client())
}

var _ = Describe("Cluster Controller", Serial, func() {

	BeforeEach(func() {
		shared.SetConfig(nil)
		shared.IPAM = shared.NewIpam()
		shared.ClusterWatch = make(chan event.GenericEvent, 1024)
	})

	It("should not create a ClusterConfig if the cluster does not match any rule's selector", func() {
		env, pc := defaultTestSetup("testdata", "test-01")

		req := testutils.RequestFromStrings("cluster-nomatch", "test")
		env.ShouldReconcile(req)

		c := &clustersv1alpha1.Cluster{}
		Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: "cluster-nomatch", Namespace: "test"}, c)).To(Succeed())

		ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccs).To(BeEmpty())

		verifyAppliedRulesAnnotationConsistency(env, pc, c, ccs)
	})

	It("should assign disjunct CIDRs to clusters", func() {
		env, pc := defaultTestSetup("testdata", "test-01")

		cidrs := []string{}
		for _, cName := range []string{"cluster-0", "cluster-1"} {
			req := testutils.RequestFromStrings(cName, "test")
			env.ShouldReconcile(req)
			c := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: cName, Namespace: "test"}, c)).To(Succeed())
			ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
			Expect(err).ToNot(HaveOccurred())
			Expect(ccs).To(HaveLen(1))
			for _, cc := range ccs {
				Expect(cc.Spec.Patches).To(ConsistOf(
					MatchFields(IgnoreExtras, Fields{
						"Path": Equal("/spec/networking/nodes"),
						"Op":   Equal("add"),
						"Value": WithTransform(func(j *apiextensionsv1.JSON) string {
							var s string
							Expect(json.Unmarshal(j.Raw, &s)).To(Succeed())
							return s
						}, And(HavePrefix("10.0."), HaveSuffix(".0/24"))),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Path": Equal("/spec/networking/services"),
						"Op":   Equal("add"),
						"Value": WithTransform(func(j *apiextensionsv1.JSON) string {
							var s string
							Expect(json.Unmarshal(j.Raw, &s)).To(Succeed())
							return s
						}, And(HavePrefix("10.0."), HaveSuffix(".0/24"))),
					}),
				))
				for _, patch := range cc.Spec.Patches {
					var cidrString string
					Expect(json.Unmarshal(patch.Value.Raw, &cidrString)).To(Succeed())
					cidrs = append(cidrs, cidrString)
				}
			}
			verifyAppliedRulesAnnotationConsistency(env, pc, c, ccs)
		}

		parent := "10.0.0.0/16"
		// all CIDRs should be unique
		Expect(cidrs).To(ConsistOf(sets.New(cidrs...).UnsortedList()))
		// all CIDRs should overlap with the parent and none of them should overlap with each other
		for i, c1 := range cidrs {
			Expect(goipam.PrefixesOverlapping([]string{parent}, []string{c1})).To(HaveOccurred())
			Expect(goipam.PrefixesOverlapping(excludeIndex(cidrs, i), []string{c1})).ToNot(HaveOccurred())
		}
	})

	It("should correctly assign nested CIDRs where configured", func() {
		env, pc := defaultTestSetup("testdata", "test-01")

		req := testutils.RequestFromStrings("cluster-2", "test")
		env.ShouldReconcile(req)

		c := &clustersv1alpha1.Cluster{}
		Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: "cluster-2", Namespace: "test"}, c)).To(Succeed())

		ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccs).To(HaveLen(1))
		verifyAppliedRulesAnnotationConsistency(env, pc, c, ccs)
		cc := ccs["nested"]
		Expect(cc).ToNot(BeNil())
		parent := "10.0.0.0/16"
		nodesCidr := ""
		podsCidr := ""
		servicesCidr := ""
		for _, patch := range cc.Spec.Patches {
			var cidrString string
			Expect(json.Unmarshal(patch.Value.Raw, &cidrString)).To(Succeed())
			switch patch.Path {
			case "/spec/networking/nodes":
				nodesCidr = cidrString
			case "/spec/networking/pods":
				podsCidr = cidrString
			case "/spec/networking/services":
				servicesCidr = cidrString
			default:
				Fail(fmt.Sprintf("unexpected patch path: %s", patch.Path))
			}
		}

		// this time, every CIDR should overlap with every other CIDR, since they are all nested
		cidrs := []string{parent, nodesCidr, podsCidr, servicesCidr}
		for i, c1 := range cidrs {
			Expect(goipam.PrefixesOverlapping(excludeIndex(cidrs, i), []string{c1})).To(HaveOccurred())
		}

		nodes, err := shared.IPAM.PrefixFrom(env.Ctx, nodesCidr)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes.ParentCidr).ToNot(BeEmpty())
		Expect(nodes.ParentCidr).To(Equal(parent))

		pods, err := shared.IPAM.PrefixFrom(env.Ctx, podsCidr)
		Expect(err).ToNot(HaveOccurred())
		Expect(pods.ParentCidr).ToNot(BeEmpty())
		Expect(pods.ParentCidr).To(Equal(nodes.Cidr))

		services, err := shared.IPAM.PrefixFrom(env.Ctx, servicesCidr)
		Expect(err).ToNot(HaveOccurred())
		Expect(services.ParentCidr).ToNot(BeEmpty())
		Expect(services.ParentCidr).To(Equal(pods.Cidr))
	})

	It("should correctly consider all globally defined parent CIDRs when a rule does not specify any parents", func() {
		env, pc := defaultTestSetup("testdata", "test-01")

		req := testutils.RequestFromStrings("cluster-3", "test")
		env.ShouldReconcile(req)

		c := &clustersv1alpha1.Cluster{}
		Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: "cluster-3", Namespace: "test"}, c)).To(Succeed())

		ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccs).To(HaveLen(1))
		verifyAppliedRulesAnnotationConsistency(env, pc, c, ccs)
		cc := ccs["noparent"]
		Expect(cc).ToNot(BeNil())
		Expect(cc.Spec.Patches).To(HaveLen(1))

		var cidrString string
		Expect(json.Unmarshal(cc.Spec.Patches[0].Value.Raw, &cidrString)).To(Succeed())
		Expect(goipam.PrefixesOverlapping([]string{"10.0.0.0/16"}, []string{cidrString})).To(HaveOccurred())
	})

	It("should use a subsequent CIDR from the same parent if the previous ones are exhausted", func() {
		env, pc := defaultTestSetup("testdata", "test-02")

		clusters := []*clustersv1alpha1.Cluster{}
		for i := 0; i <= 4; i++ {
			cName := fmt.Sprintf("cluster-%d", i)
			req := testutils.RequestFromStrings(cName, "test")
			env.ShouldReconcile(req)
			c := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: cName, Namespace: "test"}, c)).To(Succeed())
			clusters = append(clusters, c)
		}

		ccs := make([][]*gardenv1alpha1.ClusterConfig, len(clusters))
		for i, c := range clusters {
			ccList, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
			Expect(err).ToNot(HaveOccurred())
			ccs[i] = slices.Collect(maps.Values(ccList))
			verifyAppliedRulesAnnotationConsistency(env, pc, c, ccList)
		}

		// all clusters should have received a CIDR
		// cluster 0 and 1 should have CIDRs starting with 10.x, while 2 and 3 should have CIDRs starting with 11.x, and 4 should have a CIDR starting with 12.x
		for i, ccList := range ccs {
			Expect(ccList).To(HaveLen(1))
			cc := ccList[0]
			Expect(cc.Name).To(ContainSubstring("multi"))
			Expect(cc.Spec.Patches).To(HaveLen(1))
			var cidrString string
			Expect(json.Unmarshal(cc.Spec.Patches[0].Value.Raw, &cidrString)).To(Succeed())
			Expect(cidrString).To(HavePrefix(fmt.Sprintf("1%d.", i/2)))
		}
	})

	It("should use a subsequent parent CIDR set specified in the rule if the previous ones are exhausted", func() {
		env, pc := defaultTestSetup("testdata", "test-02")

		clusters := []*clustersv1alpha1.Cluster{}
		for i := 0; i <= 4; i++ {
			cName := fmt.Sprintf("cluster-%d", i)
			c := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: cName, Namespace: "test"}, c)).To(Succeed())
			c.Spec.Purposes = []string{"combi"} // use the other rule
			Expect(env.Client().Update(env.Ctx, c)).To(Succeed())
			req := testutils.RequestFromStrings(cName, "test")
			env.ShouldReconcile(req)
			Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
			clusters = append(clusters, c)
		}

		ccs := make([][]*gardenv1alpha1.ClusterConfig, len(clusters))
		for i, c := range clusters {
			ccList, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
			Expect(err).ToNot(HaveOccurred())
			ccs[i] = slices.Collect(maps.Values(ccList))
			verifyAppliedRulesAnnotationConsistency(env, pc, c, ccList)
		}

		// all clusters should have received a CIDR
		// cluster 0 and 1 should have CIDRs starting with 10.x, while 2 and 3 should have CIDRs starting with 11.x, and 4 should have a CIDR starting with 12.x
		for i, ccList := range ccs {
			Expect(ccList).To(HaveLen(1))
			cc := ccList[0]
			Expect(cc.Name).To(ContainSubstring("combi"))
			Expect(cc.Spec.Patches).To(HaveLen(1))
			var cidrString string
			Expect(json.Unmarshal(cc.Spec.Patches[0].Value.Raw, &cidrString)).To(Succeed())
			Expect(cidrString).To(HavePrefix(fmt.Sprintf("1%d.", i/2)))
		}
	})

	It("should return an error if no more CIDRs are available to assign", func() {
		env, _ := defaultTestSetup("testdata", "test-03")

		for i := range 2 {
			cName := fmt.Sprintf("cluster-%d", i)
			req := testutils.RequestFromStrings(cName, "test")
			env.ShouldReconcile(req)
		}

		// the configuration only allows CIDRs for two clusters to be assigned, so the third cluster should not receive a CIDR
		req := testutils.RequestFromStrings("cluster-2", "test")
		env.ShouldNotReconcileWithError(req, MatchError(And(ContainSubstring("unable to generate CIDR"), ContainSubstring("rule-small"))))
	})

	It("should free the assigned CIDRs when a cluster gets deleted", func() {
		env, pc := defaultTestSetup("testdata", "test-03")

		for i := range 2 {
			cName := fmt.Sprintf("cluster-%d", i)
			req := testutils.RequestFromStrings(cName, "test")
			env.ShouldReconcile(req)
		}

		// all CIDRs are assigned now
		// deleting a cluster should free up one, so that another cluster can get a CIDR assigned
		c := &clustersv1alpha1.Cluster{}
		c.SetName("cluster-0")
		c.SetNamespace("test")
		Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
		ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccs).To(HaveLen(1))
		cc := ccs["rule-small"]
		c0cidr := ""
		Expect(cc.Spec.Patches).To(HaveLen(1))
		var cidrString string
		Expect(json.Unmarshal(cc.Spec.Patches[0].Value.Raw, &cidrString)).To(Succeed())
		c0cidr = cidrString

		Expect(env.Client().Delete(env.Ctx, c)).To(Succeed())
		Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(c), c)).To(MatchError(apierrors.IsNotFound, "IsNotFound"))
		env.ShouldReconcile(testutils.RequestFromObject(c))
		// ClusterConfig should be deleted and CIDR should be freed
		// but the fake client does not automatically delete objects where all owners are gone, so we cannot verify the deletion of the ClusterConfig
		// but it should not have the finalizer anymore
		err = env.Client().Get(env.Ctx, client.ObjectKeyFromObject(cc), cc)
		if err != nil {
			// must be a not found error
			Expect(err).To(MatchError(apierrors.IsNotFound, "IsNotFound"))
		} else {
			// or the finalizers must be empty
			Expect(cc.Finalizers).To(BeEmpty())
		}

		allCidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(allCidrs).ToNot(ContainElement(c0cidr))

		// a new cluster should now be able to get the freed CIDR assigned
		req := testutils.RequestFromStrings("cluster-2", "test")
		env.ShouldReconcile(req)
		Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: "cluster-2", Namespace: "test"}, c)).To(Succeed())
		ccs, err = shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccs).To(HaveLen(1))
		cc = ccs["rule-small"]
		Expect(cc.Spec.Patches).To(HaveLen(1))
		Expect(json.Unmarshal(cc.Spec.Patches[0].Value.Raw, &cidrString)).To(Succeed())
		Expect(cidrString).To(Equal(c0cidr))
	})

	It("should not free any CIDRs if the Cluster still has finalizers", func() {
		env, pc := defaultTestSetup("testdata", "test-03")

		c := &clustersv1alpha1.Cluster{}
		c.SetName("cluster-0")
		c.SetNamespace("test")
		env.ShouldReconcile(testutils.RequestFromObject(c))
		Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
		ccs, err := shared.FetchClusterConfigsForCluster(env.Ctx, pc, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccs).To(HaveLen(1))
		cc := ccs["rule-small"]
		Expect(cc).ToNot(BeNil())
		allCidrs, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
		Expect(err).ToNot(HaveOccurred())

		// patch a finalizer to the Cluster, so that it cannot be deleted
		c.Finalizers = []string{"test-finalizer"}
		Expect(env.Client().Update(env.Ctx, c)).To(Succeed())

		Expect(env.Client().Delete(env.Ctx, c)).To(Succeed())
		Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
		env.ShouldReconcile(testutils.RequestFromObject(c))

		// ClusterConfig should still exist and no CIDR should be freed
		Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(cc), cc)).To(Succeed())
		Expect(cc.Finalizers).To(ConsistOf(ipamv1alpha1.CIDRManagementFinalizer))
		cidrsAfter, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(cidrsAfter).To(ConsistOf(allCidrs))
	})

})

func excludeIndex[T any](slice []T, index int) []T {
	if index < 0 || index >= len(slice) {
		return slice
	}
	// it would be easier to do something like
	// return append(slice[:index], slice[index+1:]...)
	// but append modifies the passed-in slice, if it has remaining capacity, which is not desirable in this case
	res := make([]T, len(slice)-1)
	copy(res, slice[:index])
	copy(res[index:], slice[index+1:])
	return res
}

// verifyAppliedRulesAnnotationConsistency verifies that the applied rules annotation on the Cluster is consistent with the currently existing ClusterConfigs for the Cluster.
// If ccs is nil, it will be fetched.
func verifyAppliedRulesAnnotationConsistency(env *testutils.Environment, pc *clusters.Cluster, cluster *clustersv1alpha1.Cluster, ccs map[string]*gardenv1alpha1.ClusterConfig) {
	// fetch ClusterConfigs if not provided
	if ccs == nil {
		var err error
		ccs, err = shared.FetchClusterConfigsForCluster(env.Ctx, pc, cluster)
		Expect(err).ToNot(HaveOccurred())
	}

	// fetch the applied rules annotation
	annotationValue, ok := cluster.GetAnnotations()[ipamv1alpha1.AppliedRulesAnnotationKey]
	if !ok {
		Expect(ccs).To(BeEmpty(), "the applied rules annotation does not exist, which only should be the case if there are no ClusterConfigs for the Cluster, but there are %d ClusterConfigs", len(ccs))
		return
	}
	appliedRules := ipamv1alpha1.AppliedRulesAnnotation{}
	Expect(appliedRules.FromAnnotation(annotationValue)).To(Succeed())

	// the applied rules should be consistent with the existing ClusterConfigs
	Expect(appliedRules).To(HaveLen(len(ccs)), "number of entries in applied rules annotation should match the number of existing ClusterConfigs for the Cluster")
	for ruleID, cc := range ccs {
		annInjections, ok := appliedRules[ruleID]
		Expect(ok).To(BeTrue(), fmt.Sprintf("applied rules annotation should contain an entry for rule '%s'", ruleID))
		Expect(annInjections).To(HaveLen(len(cc.Spec.Patches)), fmt.Sprintf("number of injections in applied rules annotation for rule '%s' should match the number of patches in the corresponding ClusterConfig", ruleID))
		for _, patch := range cc.Spec.Patches {
			var cidrString string
			Expect(json.Unmarshal(patch.Value.Raw, &cidrString)).To(Succeed())
			Expect(annInjections).To(HaveKeyWithValue(patch.Path, cidrString))
		}
	}
}
