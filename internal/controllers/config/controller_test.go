package config_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/controller-utils/pkg/collections"
	testutils "github.com/openmcp-project/controller-utils/pkg/testing"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"

	"github.com/openmcp-project/platform-service-gardener-ipam/api/install"
	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/controllers/config"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

const (
	platformCluster = "platform"

	providerName = "gardener-ipam"
	environment  = "default"
)

var platformScheme = install.InstallOperatorAPIsPlatform(runtime.NewScheme())

func defaultTestSetup(testDirPathSegments ...string) *testutils.Environment {
	env := testutils.NewEnvironmentBuilder().
		WithFakeClient(platformScheme).
		WithInitObjectPath(testDirPathSegments...).
		WithDynamicObjectsWithStatus(&clustersv1alpha1.AccessRequest{}).
		WithReconcilerConstructor(func(c client.Client) reconcile.Reconciler {
			rec := config.NewIPAMConfigController(clusters.NewTestClusterFromClient(platformCluster, c), providerName, nil)
			return rec
		}).
		Build()

	return env
}

func cidrSliceToStringSlice(cidrs []ipamv1alpha1.CIDR) []string {
	result := make([]string, len(cidrs))
	for i, cidr := range cidrs {
		result[i] = string(cidr)
	}
	return result
}

var _ = Describe("Config Controller", Serial, Ordered, func() {

	BeforeAll(func() {
		shared.SetProviderName(providerName)
		shared.SetEnvironment(environment)
	})

	BeforeEach(func() {
		shared.SetConfig(nil)
		shared.IPAM = nil
		shared.ClusterWatch = make(chan event.GenericEvent, 1024)
	})

	It("should set the config", func() {
		env := defaultTestSetup("testdata", "test-01")

		req := testutils.RequestFromStrings(providerName)
		rr := env.ShouldReconcile(req)
		Expect(rr.RequeueAfter).To(BeZero())
		Expect(shared.ClusterWatch).To(BeEmpty())

		cfg := shared.GetConfig()
		Expect(cfg).ToNot(BeNil())
		Expect(cfg.Spec.ParentCIDRs).To(HaveKeyWithValue("foo", WithTransform(cidrSliceToStringSlice, ConsistOf("10.0.0.0/16"))))
	})

	It("should requeue if periodic state refresh is configured", func() {
		env := defaultTestSetup("testdata", "test-02")

		req := testutils.RequestFromStrings(providerName)
		rr := env.ShouldReconcile(req)
		Expect(rr.RequeueAfter).To(BeNumerically("~", time.Hour, time.Second))
		Expect(shared.ClusterWatch).To(BeEmpty())
	})

	It("should trigger reconciliations for relevant clusters when the injection rules change", func() {
		env := defaultTestSetup("testdata", "test-03")

		req := testutils.RequestFromStrings(providerName)
		rr := env.ShouldReconcile(req)
		Expect(rr.RequeueAfter).To(BeZero())

		cfg := shared.GetConfig()
		Expect(cfg).ToNot(BeNil())

		Expect(shared.ClusterWatch).ToNot(BeEmpty())
		events := []event.GenericEvent{}
		// read all events from the channel
		done := false
		for !done {
			select {
			case e := <-shared.ClusterWatch:
				events = append(events, e)
			default:
				done = true
			}
		}
		Expect(events).To(WithTransform(func(events []event.GenericEvent) []string {
			return collections.ProjectSliceToSlice(events, func(elem event.GenericEvent) string {
				return elem.Object.GetNamespace() + "/" + elem.Object.GetName()
			})
		}, ConsistOf("test/cluster-0", "test/cluster-1", "test/cluster-2")))

		// simulate config change
		cfg.Spec.InjectionRules = []ipamv1alpha1.CIDRInjection{cfg.Spec.InjectionRules[0]}
		Expect(env.Client().Update(env.Ctx, cfg)).To(Succeed())

		rr = env.ShouldReconcile(req)
		Expect(rr.RequeueAfter).To(BeZero())
		Expect(shared.ClusterWatch).ToNot(BeEmpty())
		events = []event.GenericEvent{}
		// read all events from the channel
		done = false
		for !done {
			select {
			case e := <-shared.ClusterWatch:
				events = append(events, e)
			default:
				done = true
			}
		}
		Expect(events).To(WithTransform(func(events []event.GenericEvent) []string {
			return collections.ProjectSliceToSlice(events, func(elem event.GenericEvent) string {
				return elem.Object.GetNamespace() + "/" + elem.Object.GetName()
			})
		}, ConsistOf("test/cluster-0", "test/cluster-1")))
	})

})
