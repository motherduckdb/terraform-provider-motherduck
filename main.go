package main

import (
	"context"
	"flag"
	"log"

	mdprovider "github.com/motherduckdb/terraform-provider-motherduck/internal/provider"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "run provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), mdprovider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/motherduckdb/motherduck",
		Debug:   *debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
