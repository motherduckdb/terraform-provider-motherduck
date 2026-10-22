//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

var (
	diveContractTitleArg       = regexp.MustCompile(`title := '((?:[^']|'')*)'`)
	diveContractContentArg     = regexp.MustCompile(`content := '((?:[^']|'')*)'`)
	diveContractDescriptionArg = regexp.MustCompile(`description := '((?:[^']|'')*)'`)
	diveContractMountArg       = regexp.MustCompile(`\{alias: '((?:[^']|'')*)', url: '((?:[^']|'')*)'\}`)
)

type diveContractMount struct {
	Name         string `json:"name"`
	Alias        string `json:"alias"`
	URL          string `json:"url"`
	ID           string `json:"id"`
	ResourceType string `json:"resource_type"`
}

// diveVersionContractSQL keeps one Dive with its current version. Like
// MotherDuck, MD_CREATE_DIVE stores no version description, a content update
// writes a new version, and an empty version description reads as null.
type diveVersionContractSQL struct {
	*contractSQL

	diveMu             sync.Mutex
	exists             bool
	title              string
	content            string
	version            int64
	versionDescription *string
	mounts             []diveContractMount
	contentWrites      []string
}

func (c *diveVersionContractSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function check %q", query)
	}
	switch fmt.Sprint(args[0]) {
	case "md_create_dive", "md_get_dive", "md_delete_dive", "md_update_dive_content", "md_update_dive_metadata":
		return true, nil
	case "md_update_dive_status":
		return false, nil
	}
	return false, fmt.Errorf("unexpected function check for %q", args[0])
}

func diveContractString(pattern *regexp.Regexp, query string) (string, bool) {
	match := pattern.FindStringSubmatch(query)
	if match == nil {
		return "", false
	}
	return strings.ReplaceAll(match[1], "''", "'"), true
}

func diveContractMounts(query string) []diveContractMount {
	var mounts []diveContractMount
	for _, match := range diveContractMountArg.FindAllStringSubmatch(query, -1) {
		url := strings.ReplaceAll(match[2], "''", "'")
		mount := diveContractMount{Name: strings.TrimPrefix(url, "md:"), Alias: match[1], URL: url, ID: "00000000-0000-4000-8000-0000000000db", ResourceType: "database"}
		if strings.HasPrefix(url, "md:_share/") {
			mount.ResourceType = "share"
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

func (c *diveVersionContractSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.diveMu.Lock()
	defer c.diveMu.Unlock()
	switch {
	case strings.Contains(query, "FROM MD_CREATE_DIVE("):
		if c.exists {
			return flightDependencyRow{err: errors.New("duplicate Dive create")}
		}
		c.exists = true
		c.title, _ = diveContractString(diveContractTitleArg, query)
		c.content, _ = diveContractString(diveContractContentArg, query)
		c.version = 1
		c.versionDescription = nil
		c.mounts = diveContractMounts(query)
		return flightDependencyRow{values: []any{contractDiveID}}
	case strings.Contains(query, "FROM MD_GET_DIVE("):
		if !c.exists {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		mounts, _ := json.Marshal(c.mounts)
		if c.mounts == nil {
			mounts = []byte("[]")
		}
		var versionDescription any
		if c.versionDescription != nil {
			versionDescription = *c.versionDescription
		}
		return flightDependencyRow{values: []any{
			c.title, nil, c.version, "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z",
			"00000000-0000-4000-8000-0000000000aa", "owner", c.content, versionDescription,
			fmt.Sprintf("2026-09-01T00:00:%02dZ", c.version), int64(1), string(mounts),
		}}
	}
	return flightDependencyRow{err: fmt.Errorf("unexpected Dive query %q", query)}
}

func (c *diveVersionContractSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.diveMu.Lock()
	defer c.diveMu.Unlock()
	switch {
	case strings.Contains(query, "FROM MD_UPDATE_DIVE_CONTENT("):
		c.contentWrites = append(c.contentWrites, query)
		c.content, _ = diveContractString(diveContractContentArg, query)
		c.version++
		c.versionDescription = nil
		if description, ok := diveContractString(diveContractDescriptionArg, query); ok && description != "" {
			c.versionDescription = &description
		}
		c.mounts = diveContractMounts(query)
	case strings.Contains(query, "FROM MD_UPDATE_DIVE_METADATA("):
		if title, ok := diveContractString(diveContractTitleArg, query); ok {
			c.title = title
		}
	case strings.Contains(query, "FROM MD_DELETE_DIVE("):
		c.exists = false
	default:
		return "", fmt.Errorf("unexpected Dive JSON query %q", query)
	}
	return "[]", nil
}

func TestContractDiveVersionDescriptionAndMountMetadata(t *testing.T) {
	client := &diveVersionContractSQL{contractSQL: newContractSQL()}
	config := func(title, content, versionDescription string) string {
		description := ""
		if versionDescription != "" {
			description = fmt.Sprintf("  version_description = %q\n", versionDescription)
		}
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_dive" "test" {
  title   = %q
  content = %q
%s
  required_resources = [{
    alias = "db"
    url   = "md:analytics"
  }]
}
`, title, content, description)
	}
	attr := func(name, value string) resource.TestCheckFunc {
		return resource.TestCheckResourceAttr("motherduck_dive.test", name, value)
	}
	update := resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_dive.test", plancheck.ResourceActionUpdate)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			client.diveMu.Lock()
			defer client.diveMu.Unlock()
			if client.exists {
				return errors.New("contract Dive still exists after destroy")
			}
			// Create writes the description as version 2, the description
			// change writes version 3, and the content change writes version 4.
			if len(client.contentWrites) != 3 {
				return fmt.Errorf("content writes = %d, want 3: %q", len(client.contentWrites), client.contentWrites)
			}
			for i, want := range []string{"description := 'first notes'", "description := 'second notes'", ""} {
				got := client.contentWrites[i]
				if want == "" && strings.Contains(got, "description :=") || want != "" && !strings.Contains(got, want) {
					return fmt.Errorf("content write %d = %q, want %q", i+1, got, want)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("Dive", "v1", "first notes"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attr("current_version", "2"),
					attr("version_description", "first notes"),
					attr("version_created_at", "2026-09-01T00:00:02Z"),
					attr("owner_id", "00000000-0000-4000-8000-0000000000aa"),
					attr("required_resources.0.resource_type", "database"),
				),
			},
			{
				// A metadata change keeps the version and its description.
				Config:           config("Dive renamed", "v1", "first notes"),
				ConfigPlanChecks: update,
				Check:            resource.ComposeAggregateTestCheckFunc(attr("current_version", "2"), attr("version_description", "first notes")),
			},
			{
				Config:           config("Dive renamed", "v1", "second notes"),
				ConfigPlanChecks: update,
				Check:            resource.ComposeAggregateTestCheckFunc(attr("current_version", "3"), attr("version_description", "second notes"), attr("content", "v1")),
			},
			{
				// Without a configured description, a content change writes a
				// version that has none.
				Config:           config("Dive renamed", "v2", ""),
				ConfigPlanChecks: update,
				Check: resource.ComposeAggregateTestCheckFunc(
					attr("current_version", "4"),
					resource.TestCheckNoResourceAttr("motherduck_dive.test", "version_description"),
					attr("required_resources.0.resource_type", "database"),
				),
			},
		},
	})
}
