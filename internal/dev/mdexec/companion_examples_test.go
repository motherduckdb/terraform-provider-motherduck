package main

import (
	"os"
	"path/filepath"
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
	if bridge.Source != "terraform-provider" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(bridge.Version) || len(bridge.Parameters) != 1 {
		t.Fatal("Pulumi bridge must have a pinned version and one provider binary parameter")
	}
	path := filepath.Clean(bridge.Parameters[0])
	if filepath.IsAbs(path) || strings.HasPrefix(path, "..") || filepath.Base(path) != "terraform-provider-motherduck" {
		t.Fatal("Pulumi provider binary must resolve inside the example and retain its provider basename")
	}
	requirements, err := os.ReadFile("../../../examples/pulumi/python/requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^pulumi==\d+\.\d+\.\d+$`).Match(requirements) {
		t.Fatal("Pulumi SDK must be pinned independently of the bridge")
	}
}
