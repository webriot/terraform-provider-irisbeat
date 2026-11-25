// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"log"

	"github.com/Webriot/terraform-provider-irisbeat/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version = "dev"

func main() {
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/webriot/irisbeat", ProtocolVersion: 6,
	})
	if err != nil {
		log.Fatal(err)
	}
}
