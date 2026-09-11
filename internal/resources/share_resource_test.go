package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlcatalog"
)

func TestPrepareShareCreateState(t *testing.T) {
	model := &shareModel{
		Name:      types.StringValue("share_name"),
		ID:        types.StringUnknown(),
		URL:       types.StringUnknown(),
		CreatedTS: types.StringUnknown(),
	}

	prepareShareCreateState(model)

	if got, want := model.ID.ValueString(), "share_name"; got != want {
		t.Fatalf("share id = %q, want %q", got, want)
	}
	if !model.URL.IsNull() {
		t.Fatalf("share URL should be known null before catalog read, got %#v", model.URL)
	}
	if !model.CreatedTS.IsNull() {
		t.Fatalf("share created_ts should be known null before catalog read, got %#v", model.CreatedTS)
	}
}

func TestApplyOwnedShare(t *testing.T) {
	ctx := context.Background()
	liveDefaults := sqlcatalog.OwnedShare{
		URL:            sqlNullString("md:_share/share_name/uuid"),
		SourceDatabase: sqlNullString("source_db"),
		Access:         sqlNullString("ORGANIZATION"),
		Visibility:     sqlNullString("DISCOVERABLE"),
		UpdateMode:     sqlNullString("AUTOMATIC"),
		IncludePattern: sqlNullStringInvalid(),
		CreatedTS:      sqlNullString("2026-01-01 00:00:00"),
	}
	tests := map[string]struct {
		model shareModel
		share sqlcatalog.OwnedShare
		want  shareModel
	}{
		"omitted options read back server defaults": {
			model: shareModel{
				Name:       types.StringValue("share_name"),
				Access:     types.StringNull(),
				Visibility: types.StringNull(),
				UpdateMode: types.StringNull(),
			},
			share: liveDefaults,
			want:  shareModel{Access: types.StringValue("organization"), Visibility: types.StringValue("discoverable"), UpdateMode: types.StringValue("automatic")},
		},
		"configured options refresh lowercased from live": {
			model: shareModel{
				Name:       types.StringValue("share_name"),
				Access:     types.StringValue("restricted"),
				Visibility: types.StringValue("hidden"),
				UpdateMode: types.StringValue("manual"),
			},
			share: sqlcatalog.OwnedShare{
				URL:            liveDefaults.URL,
				SourceDatabase: liveDefaults.SourceDatabase,
				Access:         sqlNullString("RESTRICTED"),
				Visibility:     sqlNullString("HIDDEN"),
				UpdateMode:     sqlNullString("MANUAL"),
				CreatedTS:      liveDefaults.CreatedTS,
			},
			want: shareModel{
				Access:     types.StringValue("restricted"),
				Visibility: types.StringValue("hidden"),
				UpdateMode: types.StringValue("manual"),
			},
		},
		"configured options surface live drift": {
			model: shareModel{
				Name:       types.StringValue("share_name"),
				Access:     types.StringValue("restricted"),
				Visibility: types.StringValue("hidden"),
				UpdateMode: types.StringValue("manual"),
			},
			share: liveDefaults,
			want: shareModel{
				Access:     types.StringValue("organization"),
				Visibility: types.StringValue("discoverable"),
				UpdateMode: types.StringValue("automatic"),
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			applyOwnedShare(ctx, &tc.model, tc.share, &diags)
			if diags.HasError() {
				t.Fatalf("applyOwnedShare diagnostics: %v", diags)
			}
			if got, want := tc.model.ID, types.StringValue("share_name"); !got.Equal(want) {
				t.Fatalf("id = %#v, want %#v", got, want)
			}
			if !tc.model.Access.Equal(tc.want.Access) {
				t.Fatalf("access = %#v, want %#v", tc.model.Access, tc.want.Access)
			}
			if !tc.model.Visibility.Equal(tc.want.Visibility) {
				t.Fatalf("visibility = %#v, want %#v", tc.model.Visibility, tc.want.Visibility)
			}
			if !tc.model.UpdateMode.Equal(tc.want.UpdateMode) {
				t.Fatalf("update_mode = %#v, want %#v", tc.model.UpdateMode, tc.want.UpdateMode)
			}
		})
	}
}

func TestApplyOwnedShareImportThenConfiguredRoundTrip(t *testing.T) {
	ctx := context.Background()
	live := sqlcatalog.OwnedShare{
		URL:            sqlNullString("md:_share/share_name/uuid"),
		SourceDatabase: sqlNullString("source_db"),
		Access:         sqlNullString("RESTRICTED"),
		Visibility:     sqlNullString("HIDDEN"),
		UpdateMode:     sqlNullString("MANUAL"),
		CreatedTS:      sqlNullString("2026-01-01 00:00:00"),
	}

	// Import: only name is known, so live option values populate computed state.
	imported := shareModel{Name: types.StringValue("share_name")}
	var diags diag.Diagnostics
	applyOwnedShare(ctx, &imported, live, &diags)
	if diags.HasError() {
		t.Fatalf("import applyOwnedShare diagnostics: %v", diags)
	}
	if got, want := imported.Access, types.StringValue("restricted"); !got.Equal(want) {
		t.Fatalf("imported access = %#v, want %#v", got, want)
	}
	if got, want := imported.Visibility, types.StringValue("hidden"); !got.Equal(want) {
		t.Fatalf("imported visibility = %#v, want %#v", got, want)
	}
	if got, want := imported.UpdateMode, types.StringValue("manual"); !got.Equal(want) {
		t.Fatalf("imported update_mode = %#v, want %#v", got, want)
	}
	if got, want := imported.SourceDatabase, types.StringValue("source_db"); !got.Equal(want) {
		t.Fatalf("source_database = %#v, want %#v", got, want)
	}

	// Follow-up apply with the options configured refreshes them from live.
	configured := imported
	configured.Access = types.StringValue("restricted")
	configured.Visibility = types.StringValue("hidden")
	configured.UpdateMode = types.StringValue("manual")
	applyOwnedShare(ctx, &configured, live, &diags)
	if diags.HasError() {
		t.Fatalf("configured applyOwnedShare diagnostics: %v", diags)
	}
	if got, want := configured.Access, types.StringValue("restricted"); !got.Equal(want) {
		t.Fatalf("access = %#v, want %#v", got, want)
	}
	if got, want := configured.Visibility, types.StringValue("hidden"); !got.Equal(want) {
		t.Fatalf("visibility = %#v, want %#v", got, want)
	}
	if got, want := configured.UpdateMode, types.StringValue("manual"); !got.Equal(want) {
		t.Fatalf("update_mode = %#v, want %#v", got, want)
	}
}

func TestShareURLIsSensitive(t *testing.T) {
	var resp resource.SchemaResponse
	NewShareResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["url"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("url attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["url"])
	}
	if !attr.Sensitive {
		t.Fatal("share url must be sensitive because unrestricted share URLs can grant access")
	}
}

func TestShareOptionsAreOptionalComputed(t *testing.T) {
	var resp resource.SchemaResponse
	NewShareResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	for _, name := range []string{"access", "visibility", "update_mode"} {
		attr, ok := resp.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s attribute = %T, want schema.StringAttribute", name, resp.Schema.Attributes[name])
		}
		if !attr.Optional || !attr.Computed {
			t.Fatalf("%s must be optional and computed for import-safe live defaults", name)
		}
	}
}

func TestShareIncludePatternSQL(t *testing.T) {
	value, valueDiags := types.ListValueFrom(context.Background(), types.StringType, []string{"main.reporting_*", `main."dim,region"`})
	if valueDiags.HasError() {
		t.Fatalf("list diagnostics: %v", valueDiags)
	}
	var diags diag.Diagnostics
	got, ok := shareIncludePattern(context.Background(), value, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("shareIncludePattern diagnostics: %v", diags)
	}
	if got != `main.reporting_*,main."dim,region"` {
		t.Fatalf("include pattern = %q", got)
	}
}

func TestValidateShareIncludePatternLength(t *testing.T) {
	valid := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("main.*"),
	})
	var validDiags diag.Diagnostics
	validateShareIncludePattern(valid, &validDiags)
	if validDiags.HasError() {
		t.Fatalf("valid include pattern diagnostics: %v", validDiags)
	}

	tooLong := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue(strings.Repeat("x", 16385)),
	})
	var invalidDiags diag.Diagnostics
	validateShareIncludePattern(tooLong, &invalidDiags)
	if !invalidDiags.HasError() {
		t.Fatal("expected oversized include pattern diagnostics")
	}
}
