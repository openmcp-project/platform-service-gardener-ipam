package main

import (
	"fmt"
	"os"

	"github.com/openmcp-project/platform-service-gardener-ipam/cmd/platform-service-gardener-ipam/app"
)

func main() {
	cmd := app.NewPlatformServiceGardenerIPAMCommand()

	if err := cmd.Execute(); err != nil {
		fmt.Print(err)
		os.Exit(1)
	}
}
