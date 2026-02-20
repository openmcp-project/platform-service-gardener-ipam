package cluster_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openmcp-project/platform-service-gardener-ipam/api/install"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/shared"
)

const (
	platformCluster = "platform"

	providerName = "ipam"
	environment  = "test"
)

var platformScheme = install.InstallOperatorAPIsPlatform(runtime.NewScheme())

func TestClusterController(t *testing.T) {
	RegisterFailHandler(Fail)

	shared.SetProviderName(providerName)
	shared.SetEnvironment(environment)

	RunSpecs(t, "IPAM Cluster Controller Test Suite")
}
