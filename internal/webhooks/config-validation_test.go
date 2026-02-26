package webhooks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/yaml"

	ipamv1alpha1 "github.com/openmcp-project/platform-service-gardener-ipam/api/ipam/v1alpha1"
	"github.com/openmcp-project/platform-service-gardener-ipam/internal/webhooks"
)

var validationTestDataDir = filepath.Join("testdata", "config-validation")

var _ = Describe("Config Validation Logic", func() {

	configs := map[string]*ipamv1alpha1.IPAMConfig{}
	dirEntries, err := os.ReadDir(validationTestDataDir)
	Expect(err).ToNot(HaveOccurred())
	for _, entry := range dirEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(validationTestDataDir, entry.Name()))
		Expect(err).ToNot(HaveOccurred())

		cfg := &ipamv1alpha1.IPAMConfig{}
		Expect(yaml.Unmarshal(data, cfg)).To(Succeed())

		configs[entry.Name()] = cfg
	}

	DescribeTableSubtree("",
		func(description string, filenamePrefix string, expectedErrors ...string) {
			configSubset := map[string]*ipamv1alpha1.IPAMConfig{}
			for name, cfg := range configs {
				if strings.HasPrefix(name, filenamePrefix) {
					configSubset[name] = cfg
				}
			}

			args := []any{
				func(cfg *ipamv1alpha1.IPAMConfig, expectedErrors []string) {
					errs := webhooks.ValidateConfig(cfg)
					if len(expectedErrors) == 0 {
						Expect(errs).ToNot(HaveOccurred())
					} else {
						for _, expectedErr := range expectedErrors {
							Expect(errs).To(MatchError(ContainSubstring(expectedErr)))
						}
					}
				},
			}
			for name, cfg := range configSubset {
				args = append(args, Entry(fmt.Sprintf("%s [%s]", description, name), cfg, expectedErrors))
			}

			DescribeTable("", args...)
		},
		func(description string, _ string, _ ...string) string {
			return description
		},
		// Arguments to entry:
		//   1. nil (usually description, but for technical reasons, the second argument is used for that)
		//   2. filename prefix to select the config(s) to test
		//   every other argument: expected error substrings, or none, if no error is expected
		Entry(nil, "should not return errors for valid configs", "valid"),
		Entry(nil, "should reject identical parent CIDRs", "identical-parents", "10.27.0.0/16", "overlaps"),
		Entry(nil, "should reject overlapping parent CIDRs", "overlapping-parents", "10.27.0.0/16", "10.27.0.64/16", "overlaps"),
		Entry(nil, "should reject injections that refer to unknown parent IDs", "unknown-parent", "my-rule", "parent", "asdf"),
		Entry(nil, "should reject injections that have IDs which conflict with parent IDs", "injection-parent-id-duplicate", "foo", "injection ID cannot be the same as a parent CIDRs ID", "foo"),
		Entry(nil, "should reject injections that have duplicate IDs", "injection-id-duplicate", "asdf", "Duplicate"),
		Entry(nil, "should reject rules which have multiple injections injecting into the same path", "injection-path-duplicate", "same path", ".spec.network.cidr"),
		Entry(nil, "should reject injections that request subnet sizes bigger than their possible parents (from global parents)", "subnet-size-parent", "parent", "bar", "24", "subnet size"),
		Entry(nil, "should reject injections that request subnet sizes bigger than their possible parents (from local parents)", "subnet-size-injection", "parent", "20", "subnet size"),
	)

})
