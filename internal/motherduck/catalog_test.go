package motherduck

import (
	"context"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/provider"
	"gopkg.in/yaml.v3"
)

func TestCatalogMentionsRegisteredProviderSurfaces(t *testing.T) {
	body, err := os.ReadFile("catalog.yaml")
	if err != nil {
		t.Fatalf("reading catalog manifest: %v", err)
	}
	var catalog catalogManifest
	if err := yaml.Unmarshal(body, &catalog); err != nil {
		t.Fatalf("parsing catalog manifest: %v", err)
	}
	catalogResources, catalogDataSources, catalogEphemeralResources := catalogSurfaces(catalog)

	providerInstance := provider.New("test")()
	registeredResources := map[string]bool{}
	for _, factory := range providerInstance.Resources(context.Background()) {
		item := factory()
		var resp resource.MetadataResponse
		item.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "motherduck"}, &resp)
		registeredResources[resp.TypeName] = true
		if !catalogResources[resp.TypeName] {
			t.Errorf("catalog.yaml does not mention registered resource %q", resp.TypeName)
		}
	}
	for surface := range catalogResources {
		if !registeredResources[surface] {
			t.Errorf("catalog.yaml mentions unregistered resource %q", surface)
		}
	}

	registeredDataSources := map[string]bool{}
	for _, factory := range providerInstance.DataSources(context.Background()) {
		item := factory()
		var resp datasource.MetadataResponse
		item.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "motherduck"}, &resp)
		registeredDataSources[resp.TypeName] = true
		if !catalogDataSources[resp.TypeName] {
			t.Errorf("catalog.yaml does not mention registered data source %q", resp.TypeName)
		}
	}
	for surface := range catalogDataSources {
		if !registeredDataSources[surface] {
			t.Errorf("catalog.yaml mentions unregistered data source %q", surface)
		}
	}

	ephemeralProvider, ok := providerInstance.(interface {
		EphemeralResources(context.Context) []func() ephemeral.EphemeralResource
	})
	if !ok {
		t.Fatal("provider does not expose ephemeral resources")
	}
	registeredEphemeralResources := map[string]bool{}
	for _, factory := range ephemeralProvider.EphemeralResources(context.Background()) {
		item := factory()
		var resp ephemeral.MetadataResponse
		item.Metadata(context.Background(), ephemeral.MetadataRequest{ProviderTypeName: "motherduck"}, &resp)
		registeredEphemeralResources[resp.TypeName] = true
		if !catalogEphemeralResources[resp.TypeName] {
			t.Errorf("catalog.yaml does not mention registered ephemeral resource %q", resp.TypeName)
		}
	}
	for surface := range catalogEphemeralResources {
		if !registeredEphemeralResources[surface] {
			t.Errorf("catalog.yaml mentions unregistered ephemeral resource %q", surface)
		}
	}
}

type catalogManifest struct {
	REST []struct {
		ProviderSurface yaml.Node `yaml:"provider_surface"`
	} `yaml:"rest"`
	SQL struct {
		Resources   []string `yaml:"resources"`
		DataSources []string `yaml:"data_sources"`
	} `yaml:"sql"`
}

func catalogSurfaces(catalog catalogManifest) (map[string]bool, map[string]bool, map[string]bool) {
	resources := map[string]bool{}
	dataSources := map[string]bool{}
	ephemeralResources := map[string]bool{}
	for _, surface := range catalog.SQL.Resources {
		resources[surface] = true
	}
	for _, surface := range catalog.SQL.DataSources {
		dataSources[surface] = true
	}
	for _, entry := range catalog.REST {
		for _, surface := range providerSurfaceValues(entry.ProviderSurface) {
			switch {
			case len(surface) > len("resource.") && surface[:len("resource.")] == "resource.":
				resources[surface[len("resource."):]] = true
			case len(surface) > len("data_source.") && surface[:len("data_source.")] == "data_source.":
				dataSources[surface[len("data_source."):]] = true
			case len(surface) > len("ephemeral_resource.") && surface[:len("ephemeral_resource.")] == "ephemeral_resource.":
				ephemeralResources[surface[len("ephemeral_resource."):]] = true
			}
		}
	}
	return resources, dataSources, ephemeralResources
}

func providerSurfaceValues(node yaml.Node) []string {
	switch node.Kind {
	case yaml.ScalarNode:
		return []string{node.Value}
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			values = append(values, providerSurfaceValues(*child)...)
		}
		return values
	default:
		return nil
	}
}
