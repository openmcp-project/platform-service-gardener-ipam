package webhooks_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/webhooks"
)

var mutationTestDataDir = filepath.Join("testdata", "cluster-mutation")

var _ = Describe("Cluster Mutation Logic", Serial, func() {

	ri := &webhooks.ReferenceInjector{}

	It("should return an error if the config is missing", func() {
		cluster := clusterWithPurposes()
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(MatchError(ContainSubstring("config")))
	})

	It("should not modify the cluster if it does not match any rule", func() {
		setConfig("test-01.yaml")
		cluster := clusterWithPurposes("asdf")
		ctx := context.Background()
		oldCluster := cluster.DeepCopy()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		Expect(cluster).To(Equal(oldCluster))
	})

	It("should inject the correct reference if the cluster matches a rule", func() {
		setConfig("test-01.yaml")
		cluster := clusterWithPurposes("test")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName}))
	})

	It("should not inject the same reference multiple times", func() {
		setConfig("test-01.yaml")
		cluster := clusterWithPurposes("test")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName}))
	})

	It("should correctly inject multiple references if the cluster matches multiple rules", func() {
		setConfig("test-02.yaml")
		cluster := clusterWithPurposes("test")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName1, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		refName2, err := shared.GenerateClusterConfigName(cluster, "another")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}))
	})

	It("should correctly add another reference if the config changes to match another rule", func() {
		setConfig("test-01.yaml")
		cluster := clusterWithPurposes("test")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName1, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}))
		setConfig("test-02.yaml")
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName2, err := shared.GenerateClusterConfigName(cluster, "another")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}))
	})

	It("should correctly add another reference if the cluster changes to match another rule", func() {
		setConfig("test-02.yaml")
		cluster := clusterWithPurposes("test")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName1, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		refName2, err := shared.GenerateClusterConfigName(cluster, "another")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}))
		cluster.Spec.Purposes = append(cluster.Spec.Purposes, "asdf")
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName3, err := shared.GenerateClusterConfigName(cluster, "third")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}, commonapi.LocalObjectReference{Name: refName3}))
	})

	It("should not remove any references if the config changes so that the cluster matches fewer rules", func() {
		setConfig("test-02.yaml")
		cluster := clusterWithPurposes("test")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName1, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		refName2, err := shared.GenerateClusterConfigName(cluster, "another")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}))
		setConfig("test-01.yaml")
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}))
	})

	It("should not remove any references if the cluster changes so that it matches fewer rules", func() {
		setConfig("test-02.yaml")
		cluster := clusterWithPurposes("test", "asdf")
		ctx := context.Background()
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		refName1, err := shared.GenerateClusterConfigName(cluster, "test")
		Expect(err).ToNot(HaveOccurred())
		refName2, err := shared.GenerateClusterConfigName(cluster, "another")
		Expect(err).ToNot(HaveOccurred())
		refName3, err := shared.GenerateClusterConfigName(cluster, "third")
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}, commonapi.LocalObjectReference{Name: refName3}))
		cluster.Spec.Purposes = []string{"test"}
		Expect(ri.Default(ctx, cluster)).To(Succeed())
		Expect(cluster.Spec.ClusterConfigs).To(ConsistOf(commonapi.LocalObjectReference{Name: refName1}, commonapi.LocalObjectReference{Name: refName2}, commonapi.LocalObjectReference{Name: refName3}))
	})

})

func clusterWithPurposes(purposes ...string) *clustersv1alpha1.Cluster {
	return &clustersv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "bar",
		},
		Spec: clustersv1alpha1.ClusterSpec{
			Purposes: purposes,
		},
	}
}

func setConfig(cfgName string) {
	data, err := os.ReadFile(filepath.Join(mutationTestDataDir, cfgName))
	Expect(err).ToNot(HaveOccurred())

	cfg := &ipamv1alpha1.IPAMConfig{}
	Expect(yaml.Unmarshal(data, cfg)).To(Succeed())
	Expect(webhooks.ValidateConfig(cfg)).To(Succeed())
	shared.SetConfig(cfg)
}
