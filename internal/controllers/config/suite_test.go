package config_test

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

	providerName = "gardener-ipam"
	environment  = "default"
)

var platformScheme = install.InstallOperatorAPIsPlatform(runtime.NewScheme())

func TestConfigController(t *testing.T) {
	RegisterFailHandler(Fail)

	shared.SetProviderName(providerName)
	shared.SetEnvironment(environment)

	RunSpecs(t, "IPAM Config Controller Test Suite")
}
