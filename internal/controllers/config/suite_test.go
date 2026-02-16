package config_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConfigController(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "IPAM Config Controller Test Suite")
}
