package shared_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"sigs.k8s.io/controller-runtime/pkg/client"

	gardenv1alpha1 "github.com/openmcp-project/cluster-provider-gardener/api/core/v1alpha1"
	"github.com/openmcp-project/controller-utils/pkg/collections"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	openmcpconst "github.com/openmcp-project/openmcp-operator/api/constants"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

var _ = Describe("Shared Utils Functions", Serial, func() {

	Context("FetchRelevantClusters", func() {

		Context("Non-nil config", func() {

			It("should fetch the relevant clusters", func() {
				env, cfg := defaultTestSetup("testdata", "test-01")
				clusters, err := shared.FetchRelevantClusters(env.Ctx, cfg, env.Client())
				Expect(err).ToNot(HaveOccurred())

				Expect(clusters).To(WithTransform(func(clusters []*clustersv1alpha1.Cluster) []string {
					return collections.ProjectSliceToSlice(clusters, func(elem *clustersv1alpha1.Cluster) string {
						return elem.GetNamespace() + "/" + elem.GetName()
					})
				}, ConsistOf("test/cluster-0", "test/cluster-1", "test/cluster-2")))
			})

		})

		Context("Nil config", func() {

			It("should fetch the relevant clusters", func() {
				env, _ := defaultTestSetup("testdata", "test-01")
				clusters, err := shared.FetchRelevantClusters(env.Ctx, nil, env.Client())
				Expect(err).ToNot(HaveOccurred())

				Expect(clusters).To(WithTransform(func(clusters []*clustersv1alpha1.Cluster) []string {
					return collections.ProjectSliceToSlice(clusters, func(elem *clustersv1alpha1.Cluster) string {
						return elem.GetNamespace() + "/" + elem.GetName()
					})
				}, ConsistOf("test/cluster-0", "test/cluster-1", "test/cluster-2", "test/cluster-3")))
			})

		})

	})

	Context("GenerateClusterConfigForInjectionList", func() {

		It("should generate the expected ClusterConfig resource", func() {
			env, _ := defaultTestSetup("testdata", "test-01")
			cl := &clustersv1alpha1.Cluster{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Namespace: "test", Name: "cluster-3"}, cl)).To(Succeed())
			injections := map[string]string{
				"foo": "1.2.3.4/32",
				"bar": "5.6.7.8/32",
			}
			ruleID := "rule-1"

			cc, err := shared.GenerateClusterConfigForInjectionList(cl, ruleID, injections)
			Expect(err).ToNot(HaveOccurred())
			Expect(cc).ToNot(BeNil())

			Expect(cc.Name).To(Equal(fmt.Sprintf("%s--ipam--%s", cl.Name, ruleID)))
			Expect(cc.Namespace).To(Equal(cl.Namespace))
			Expect(cc.Labels).To(HaveKeyWithValue(openmcpconst.ManagedByLabel, providerName))
			Expect(cc.Labels).To(HaveKeyWithValue(openmcpconst.ManagedPurposeLabel, ipamv1alpha1.ManagedPurposeLabelValue))
			Expect(cc.Labels).To(HaveKeyWithValue(openmcpconst.EnvironmentLabel, environment))
			Expect(cc.Labels).To(HaveKeyWithValue(ipamv1alpha1.ClusterTargetLabel, cl.Name))
			Expect(cc.Labels).To(HaveKeyWithValue(ipamv1alpha1.InjectionRuleLabel, ruleID))
			Expect(cc.OwnerReferences).To(ConsistOf(MatchFields(IgnoreExtras, Fields{
				"APIVersion":         Equal(gardenv1alpha1.GroupVersion.String()),
				"Kind":               Equal("Cluster"),
				"Name":               Equal(cl.Name),
				"UID":                Equal(cl.UID),
				"Controller":         PointTo(BeTrue()),
				"BlockOwnerDeletion": PointTo(BeFalse()),
			})))

			Expect(cc.Spec.PatchOptions.CreateMissingOnAdd).To(PointTo(BeTrue()))
			Expect(cc.Spec.Patches).To(ConsistOf(
				MatchFields(IgnoreExtras, Fields{
					"Path": Equal("foo"),
					"Value": PointTo(MatchFields(IgnoreExtras, Fields{
						"Raw": WithTransform(func(raw []byte) string {
							var res string
							Expect(json.Unmarshal(raw, &res)).To(Succeed())
							return res
						}, Equal("1.2.3.4/32")),
					})),
					"Op": Equal("add"),
				}),
				MatchFields(IgnoreExtras, Fields{
					"Path": Equal("bar"),
					"Value": PointTo(MatchFields(IgnoreExtras, Fields{
						"Raw": WithTransform(func(raw []byte) string {
							var res string
							Expect(json.Unmarshal(raw, &res)).To(Succeed())
							return res
						}, Equal("5.6.7.8/32")),
					})),
					"Op": Equal("add"),
				}),
			))
			Expect(cc.Spec.Extensions).To(BeEmpty())
			Expect(cc.Spec.Resources).To(BeEmpty())
		})

	})

	Context("ReleaseCIDRsForClusterConfig", func() {

		It("should release the CIDRs for the given ClusterConfig", func() {
			env, _ := defaultTestSetup("testdata", "test-01")
			Expect(shared.RestoreIPAMFromClusterState(env.Ctx, env.Client())).To(Succeed())

			cidrsInUse, err := shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrsInUse).To(ContainElements("10.0.0.0/24"))
			oldLen := len(cidrsInUse)

			cc := &gardenv1alpha1.ClusterConfig{}
			Expect(env.Client().Get(env.Ctx, client.ObjectKey{Namespace: "test", Name: "cluster-0--ipam--rule1"}, cc)).To(Succeed())
			Expect(cc.Finalizers).To(ContainElement(ipamv1alpha1.CIDRManagementFinalizer))

			Expect(shared.ReleaseCIDRsForClusterConfig(env.Ctx, env.Client(), cc)).To(Succeed())

			Expect(env.Client().Get(env.Ctx, client.ObjectKeyFromObject(cc), cc)).To(Succeed())
			Expect(cc.Finalizers).ToNot(ContainElement(ipamv1alpha1.CIDRManagementFinalizer))
			cidrsInUse, err = shared.IPAM.ReadAllPrefixCidrs(env.Ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cidrsInUse).ToNot(ContainElements("10.0.0.0/24"))
			Expect(cidrsInUse).To(HaveLen(oldLen - 1))
		})

	})

})
