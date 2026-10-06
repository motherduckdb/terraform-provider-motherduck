package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPulumiExampleManifest(t *testing.T) {
	var manifest struct {
		Name    string `yaml:"name"`
		Runtime struct {
			Name    string `yaml:"name"`
			Options struct {
				Virtualenv string `yaml:"virtualenv"`
			} `yaml:"options"`
		} `yaml:"runtime"`
		Packages map[string]struct {
			Source     string   `yaml:"source"`
			Version    string   `yaml:"version"`
			Parameters []string `yaml:"parameters"`
		} `yaml:"packages"`
	}
	content, err := os.ReadFile("../../../examples/pulumi/python/Pulumi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	if err = decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	bridge, ok := manifest.Packages["motherduck"]
	if !ok || manifest.Name == "" || manifest.Runtime.Name != "python" || manifest.Runtime.Options.Virtualenv == "" {
		t.Fatal("Pulumi example must declare its Python runtime and MotherDuck bridge")
	}
	if bridge.Source != "terraform-provider" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(bridge.Version) || len(bridge.Parameters) != 2 {
		t.Fatal("Pulumi bridge must have a pinned version, the provider registry address, and a pinned provider version")
	}
	// Install from the Terraform Registry by exact version, so the bridge
	// verifies the published, signed release instead of a hand-placed binary.
	if bridge.Parameters[0] != "registry.terraform.io/motherduckdb/motherduck" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(bridge.Parameters[1]) {
		t.Fatalf("Pulumi provider must be the Terraform Registry address with an exact version, got %v", bridge.Parameters)
	}
	pins, err := os.ReadFile("../../../scripts/lib/pulumi-cli.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pins), `PULUMI_BRIDGE_VERSION="`+bridge.Version+`"`) {
		t.Fatalf("Pulumi example bridge %s must match PULUMI_BRIDGE_VERSION in scripts/lib/pulumi-cli.sh", bridge.Version)
	}
	requirements, err := os.ReadFile("../../../examples/pulumi/python/requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	sdk := regexp.MustCompile(`(?m)^pulumi==(\d+\.\d+\.\d+)$`).FindSubmatch(requirements)
	if sdk == nil {
		t.Fatal("Pulumi SDK must be pinned independently of the bridge")
	}
	if !strings.Contains(string(pins), `PULUMI_CLI_VERSION="`+string(sdk[1])+`"`) {
		t.Fatalf("Pulumi SDK %s must match PULUMI_CLI_VERSION in scripts/lib/pulumi-cli.sh", sdk[1])
	}
}
