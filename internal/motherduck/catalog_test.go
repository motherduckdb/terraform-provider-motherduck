package motherduck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/provider"
	"gopkg.in/yaml.v3"
)

func TestCatalogMentionsRegisteredProviderSurfaces(t *testing.T) {
	catalog := loadCatalog(t)
	catalogResources, catalogDataSources, catalogEphemeralResources, unknown := catalogSurfaces(catalog)
	for _, surface := range unknown {
		t.Errorf("catalog.yaml provider_surface %q must start with resource., data_source., or ephemeral_resource.", surface)
	}
	for kind, surfaces := range map[string]map[string]bool{
		"resources":           catalogResources,
		"data-sources":        catalogDataSources,
		"ephemeral-resources": catalogEphemeralResources,
	} {
		for name := range surfaces {
			page := filepath.Join("..", "..", "docs", kind, strings.TrimPrefix(name, "motherduck_")+".md")
			body, err := os.ReadFile(page) // #nosec G304 -- paths come from the checked-in provider catalog, not external input.
			if err != nil {
				t.Errorf("missing documentation for %s: %v", name, err)
				continue
			}
			for _, section := range []string{"## Example Usage", "## Schema"} {
				if !strings.Contains(string(body), section) {
					t.Errorf("%s lacks %s", page, section)
				}
			}
			if kind == "resources" && !strings.Contains(string(body), "## Import") {
				t.Errorf("%s must document import or explicitly explain why it is unsupported", page)
			}
		}
	}

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
		Path            string    `yaml:"path"`
		Methods         []string  `yaml:"methods"`
		ProviderSurface yaml.Node `yaml:"provider_surface"`
		Status          string    `yaml:"status"`
	} `yaml:"rest"`
	SQL struct {
		Resources   []string `yaml:"resources"`
		DataSources []string `yaml:"data_sources"`
	} `yaml:"sql"`
}

func loadCatalog(t *testing.T) catalogManifest {
	t.Helper()
	body, err := os.ReadFile("catalog.yaml")
	if err != nil {
		t.Fatalf("reading catalog manifest: %v", err)
	}
	var catalog catalogManifest
	if err := yaml.Unmarshal(body, &catalog); err != nil {
		t.Fatalf("parsing catalog manifest: %v", err)
	}
	return catalog
}

func TestCatalogRESTEntriesAreWellFormed(t *testing.T) {
	catalog := loadCatalog(t)
	clientSource, err := os.ReadFile(filepath.Join("..", "client", "rest", "client.go"))
	if err != nil {
		t.Fatalf("reading REST client: %v", err)
	}
	validStatus := map[string]bool{"stable": true, "preview": true, "experimental": true, "deprecated": true, "ephemeral": true}
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	for _, entry := range catalog.REST {
		if !validStatus[entry.Status] {
			t.Errorf("REST path %s has invalid status %q", entry.Path, entry.Status)
		}
		if len(entry.Methods) == 0 {
			t.Errorf("REST path %s lists no methods", entry.Path)
		}
		for _, method := range entry.Methods {
			if !validMethods[method] {
				t.Errorf("REST path %s has invalid method %q", entry.Path, method)
			}
		}
		if len(providerSurfaceValues(entry.ProviderSurface)) == 0 {
			t.Errorf("REST path %s names no provider surface", entry.Path)
		}
		// Every literal part of the path must appear as a string literal in
		// the REST client, which catches typos and removed endpoints.
		for _, fragment := range pathLiteralFragments(entry.Path) {
			if !strings.Contains(string(clientSource), `"`+fragment) {
				t.Errorf("REST path %s fragment %q is not used by internal/client/rest", entry.Path, fragment)
			}
		}
	}
}

func TestCatalogSurfacesRejectUnknownPrefixes(t *testing.T) {
	var catalog catalogManifest
	if err := yaml.Unmarshal([]byte("rest:\n  - provider_surface: [resouce.motherduck_typo, resource.motherduck_ok]\n"), &catalog); err != nil {
		t.Fatal(err)
	}
	resources, _, _, unknown := catalogSurfaces(catalog)
	if !resources["motherduck_ok"] {
		t.Fatal("known prefix was not parsed")
	}
	if len(unknown) != 1 || unknown[0] != "resouce.motherduck_typo" {
		t.Fatalf("unknown surfaces = %v, want the misspelled prefix", unknown)
	}
}

// pathLiteralFragments returns the literal parts of a templated REST path,
// for example "/v1/users/" and "/tokens" for /v1/users/{username}/tokens.
func pathLiteralFragments(path string) []string {
	var fragments []string
	for path != "" {
		before, after, found := strings.Cut(path, "{")
		if before != "" {
			fragments = append(fragments, before)
		}
		if !found {
			break
		}
		_, rest, _ := strings.Cut(after, "}")
		path = rest
	}
	return fragments
}

func catalogSurfaces(catalog catalogManifest) (resources, dataSources, ephemeralResources map[string]bool, unknown []string) {
	resources = map[string]bool{}
	dataSources = map[string]bool{}
	ephemeralResources = map[string]bool{}
	for _, surface := range catalog.SQL.Resources {
		resources[surface] = true
	}
	for _, surface := range catalog.SQL.DataSources {
		dataSources[surface] = true
	}
	for _, entry := range catalog.REST {
		for _, surface := range providerSurfaceValues(entry.ProviderSurface) {
			if name, ok := strings.CutPrefix(surface, "resource."); ok && name != "" {
				resources[name] = true
			} else if name, ok := strings.CutPrefix(surface, "data_source."); ok && name != "" {
				dataSources[name] = true
			} else if name, ok := strings.CutPrefix(surface, "ephemeral_resource."); ok && name != "" {
				ephemeralResources[name] = true
			} else {
				unknown = append(unknown, surface)
			}
		}
	}
	return resources, dataSources, ephemeralResources, unknown
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
