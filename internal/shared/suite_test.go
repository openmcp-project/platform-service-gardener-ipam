package shared_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testutils "github.com/openmcp-project/controller-utils/pkg/testing"

	"github.com/openmcp-project/platform-service-gardener-ipam/api/install"
	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

const (
	providerName = "ipam"
	environment  = "test"
)

var platformScheme = install.InstallOperatorAPIsPlatform(runtime.NewScheme())

func TestSharedFunctions(t *testing.T) {
	RegisterFailHandler(Fail)

	shared.SetProviderName(providerName)
	shared.SetEnvironment(environment)

	RunSpecs(t, "IPAM Shared Functions Test Suite")
}

func defaultTestSetup(testDirPathSegments ...string) (*testutils.Environment, *ipamv1alpha1.IPAMConfig) {
	env := testutils.NewEnvironmentBuilder().
		WithFakeClient(platformScheme).
		WithInitObjectPath(testDirPathSegments...).
		Build()

	cfg := &ipamv1alpha1.IPAMConfig{}
	Expect(env.Client().Get(env.Ctx, client.ObjectKey{Name: providerName}, cfg)).To(Succeed())
	shared.SetConfig(cfg)

	return env, cfg
}
