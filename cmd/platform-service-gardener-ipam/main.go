package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openmcp-project/platform-service-gardener-ipam/cmd/platform-service-gardener-ipam/app"

	"github.com/openmcp-project/controller-utils/pkg/fips"
)

func main() {
	fips.Verify(context.Background())

	cmd := app.NewPlatformServiceGardenerIPAMCommand()

	if err := cmd.Execute(); err != nil {
		fmt.Print(err)
		os.Exit(1)
	}
}
