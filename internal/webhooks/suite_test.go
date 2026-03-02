package webhooks_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWebhookLogic(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Webhook Logic Test Suite")
}
