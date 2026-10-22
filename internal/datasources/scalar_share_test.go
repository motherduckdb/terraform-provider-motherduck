package datasources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

type fakeDetailsClient struct {
	available bool
	probeErr  error
	rowsJSON  string
	rowsErr   error
	queries   []string
}

func (c *fakeDetailsClient) Exists(context.Context, string, ...any) (bool, error) {
	return c.available, c.probeErr
}

func (c *fakeDetailsClient) QueryRowsJSON(_ context.Context, query string, _ ...any) (string, error) {
	c.queries = append(c.queries, query)
	return c.rowsJSON, c.rowsErr
}

func scalarDetailsOf(t *testing.T, ds datasource.DataSource) *scalarDetails {
	t.Helper()
	scalar, ok := ds.(*scalarDataSource)
	if !ok || scalar.details == nil {
		t.Fatalf("%T has no detail columns", ds)
	}
	return scalar.details
}

func TestCurrentUserDetailsReadUserInfo(t *testing.T) {
	details := scalarDetailsOf(t, NewCurrentUserDataSource())
	client := &fakeDetailsClient{available: true, rowsJSON: `[{"user_id":"u1","username":"writer","org_id":"o1","org_name":"Analytics","org_type":"business","region":"us-east-1"}]`}
	var diags diag.Diagnostics
	values := details.read(context.Background(), client, "current_user", &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	want := map[string]string{"user_id": "u1", "username": "writer", "org_id": "o1", "org_name": "Analytics", "org_type": "business", "region": "us-east-1"}
	for name, value := range want {
		if !values[name].Equal(types.StringValue(value)) {
			t.Fatalf("%s = %v, want %s", name, values[name], value)
		}
	}
	if len(client.queries) != 1 || client.queries[0] != "SELECT * FROM md_user_info()" {
		t.Fatalf("queries = %v", client.queries)
	}
}

func TestCurrentUserDetailsToleratesOlderSessions(t *testing.T) {
	details := scalarDetailsOf(t, NewCurrentUserDataSource())

	// An older extension without the region column leaves region null.
	var diags diag.Diagnostics
	values := details.read(context.Background(), &fakeDetailsClient{available: true, rowsJSON: `[{"user_id":"u1","username":"writer","org_id":"o1","org_name":"Analytics","org_type":"business"}]`}, "current_user", &diags)
	if diags.HasError() || !values["region"].IsNull() || !values["username"].Equal(types.StringValue("writer")) {
		t.Fatalf("values = %v, diagnostics = %v", values, diags)
	}

	// A session without md_user_info leaves every detail null without querying.
	missing := &fakeDetailsClient{}
	diags = nil
	values = details.read(context.Background(), missing, "current_user", &diags)
	if diags.HasError() || len(missing.queries) != 0 {
		t.Fatalf("missing function diagnostics = %v, queries = %v", diags, missing.queries)
	}
	for name, value := range values {
		if !value.IsNull() {
			t.Fatalf("%s = %v, want null", name, value)
		}
	}

	// SaaS mode disables md_user_info. The read keeps going with a warning.
	diags = nil
	values = details.read(context.Background(), &fakeDetailsClient{available: true, rowsErr: errors.New(`Permission Error: The md_user_info function is disabled in "SaaS mode".`)}, "current_user", &diags)
	if diags.HasError() || diags.WarningsCount() != 1 || !values["org_id"].IsNull() {
		t.Fatalf("SaaS mode values = %v, diagnostics = %v", values, diags)
	}

	// Other failures still fail the read.
	diags = nil
	details.read(context.Background(), &fakeDetailsClient{available: true, rowsErr: errors.New("connection reset")}, "current_user", &diags)
	if !diags.HasError() {
		t.Fatal("unexpected md_user_info errors must fail the read")
	}
	diags = nil
	details.read(context.Background(), &fakeDetailsClient{probeErr: errors.New("probe failed")}, "current_user", &diags)
	if !diags.HasError() {
		t.Fatal("probe errors must fail the read")
	}
}

func TestVersionDetailsReadExtensionHash(t *testing.T) {
	details := scalarDetailsOf(t, NewVersionDataSource())
	var diags diag.Diagnostics
	values := details.read(context.Background(), &fakeDetailsClient{available: true, rowsJSON: `[{"md_extension_version":"v1.5.6","md_extension_hash":"9f30707f97"}]`}, "version", &diags)
	if diags.HasError() || !values["extension_hash"].Equal(types.StringValue("9f30707f97")) {
		t.Fatalf("extension_hash = %v, diagnostics = %v", values["extension_hash"], diags)
	}
	diags = nil
	values = details.read(context.Background(), &fakeDetailsClient{available: true, rowsJSON: `[]`}, "version", &diags)
	if diags.HasError() || !values["extension_hash"].IsNull() {
		t.Fatalf("empty result extension_hash = %v, diagnostics = %v", values["extension_hash"], diags)
	}
}

func TestScalarDataSourcesKeepValueAndAddDetails(t *testing.T) {
	for ds, want := range map[datasource.DataSource][]string{
		NewCurrentUserDataSource():      {"org_id", "org_name", "org_type", "region", "user_id", "username", "value"},
		NewVersionDataSource():          {"extension_hash", "value"},
		NewLiveDucklingSizeDataSource(): {"value"},
	} {
		var resp datasource.SchemaResponse
		ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
		got := make([]string, 0, len(resp.Schema.Attributes))
		for name, attr := range resp.Schema.Attributes {
			if !attr.IsComputed() || attr.IsOptional() || attr.IsRequired() {
				t.Fatalf("%T.%s must be computed only", ds, name)
			}
			got = append(got, name)
		}
		if strings.Join(sorted(got), ",") != strings.Join(want, ",") {
			t.Fatalf("%T attributes = %v, want %v", ds, sorted(got), want)
		}
	}
}

func shareConfig(t *testing.T, name, url tftypes.Value) tfsdk.Config {
	t.Helper()
	var resp datasource.SchemaResponse
	NewShareDataSource().Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	attrTypes := map[string]tftypes.Type{}
	values := map[string]tftypes.Value{}
	for attrName := range resp.Schema.Attributes {
		attrTypes[attrName] = tftypes.String
		values[attrName] = tftypes.NewValue(tftypes.String, nil)
	}
	values["name"] = name
	values["url"] = url
	return tfsdk.Config{Schema: resp.Schema, Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: attrTypes}, values)}
}

func TestShareDataSourceRequiresExactlyOneLookup(t *testing.T) {
	str := func(value string) tftypes.Value { return tftypes.NewValue(tftypes.String, value) }
	null := tftypes.NewValue(tftypes.String, nil)
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	for name, tc := range map[string]struct {
		name, url tftypes.Value
		wantErr   bool
	}{
		"name only":    {str("analytics"), null, false},
		"url only":     {null, str("md:_share/analytics/abc"), false},
		"neither":      {null, null, true},
		"both":         {str("analytics"), str("md:_share/analytics/abc"), true},
		"unknown url":  {str("analytics"), unknown, false},
		"unknown name": {unknown, null, false},
	} {
		t.Run(name, func(t *testing.T) {
			ds := NewShareDataSource().(*shareDataSource)
			var resp datasource.ValidateConfigResponse
			ds.ValidateConfig(t.Context(), datasource.ValidateConfigRequest{Config: shareConfig(t, tc.name, tc.url)}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func TestShareDataSourceSchemaKeepsURLSensitive(t *testing.T) {
	var resp datasource.SchemaResponse
	NewShareDataSource().Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	url := resp.Schema.Attributes["url"].(schema.StringAttribute)
	if !url.Sensitive || !url.Optional || !url.Computed {
		t.Fatalf("url attribute = %#v, want optional, computed, and sensitive", url)
	}
	for _, name := range []string{"source_database", "source_database_uuid", "access", "visibility", "update_mode", "created_ts"} {
		if !resp.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
}

type fakeShareRow struct {
	values []any
	err    error
}

func (r fakeShareRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(dest), len(r.values))
	}
	for i, value := range r.values {
		target := dest[i].(*stdsql.NullString)
		if value == nil {
			*target = stdsql.NullString{}
			continue
		}
		*target = stdsql.NullString{String: value.(string), Valid: true}
	}
	return nil
}

type fakeShareClient struct {
	row     fakeShareRow
	queries []string
}

func (c *fakeShareClient) QueryRow(_ context.Context, query string, _ ...any) mdsql.RowScanner {
	c.queries = append(c.queries, query)
	return c.row
}

func TestDescribeShareMapsColumns(t *testing.T) {
	client := &fakeShareClient{row: fakeShareRow{values: []any{"tenant", "md:_share/tenant/abc", "tenant_db", "00000000-0000-0000-0000-0000000000a1", "ORGANIZATION", "DISCOVERABLE", "AUTOMATIC", "2026-01-01 00:00:00+00"}}}
	share, err := describeShare(context.Background(), client, "md:_share/tenant/abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(client.queries) != 1 || client.queries[0] != "SELECT name, url, source_db_name, source_db_uuid::VARCHAR, access, visibility, update, created_ts::VARCHAR FROM MD_DESCRIBE_DATABASE_SHARE('md:_share/tenant/abc')" {
		t.Fatalf("queries = %v", client.queries)
	}
	var model shareModel
	setShareModel(&model, share)
	for name, got := range map[string]types.String{
		"tenant":                               model.Name,
		"md:_share/tenant/abc":                 model.URL,
		"tenant_db":                            model.SourceDatabase,
		"00000000-0000-0000-0000-0000000000a1": model.SourceDatabaseUUID,
		"organization":                         model.Access,
		"discoverable":                         model.Visibility,
		"automatic":                            model.UpdateMode,
		"2026-01-01 00:00:00+00":               model.CreatedTS,
	} {
		if !got.Equal(types.StringValue(name)) {
			t.Fatalf("got %v, want %s", got, name)
		}
	}
}

func TestShareErrorsNeverEchoTheURL(t *testing.T) {
	url := types.StringValue("md:_share/tenant/0a9a026ec5a55946a9de39851087ed81")
	message := redactShareURL("Catalog Error: Share: 'md:_share/tenant/0a9a026ec5a55946a9de39851087ed81' not found.", url)
	if strings.Contains(message, "0a9a026ec5a55946a9de39851087ed81") || !strings.Contains(message, "<redacted share URL>") {
		t.Fatalf("message = %q, want the URL redacted", message)
	}
	if got := redactShareURL("boom", types.StringNull()); got != "boom" {
		t.Fatalf("message without url = %q", got)
	}
	if !shareNotFound(errors.New("Catalog Error: Share: 'tenant' not found.")) || shareNotFound(errors.New("permission denied")) || shareNotFound(nil) {
		t.Fatal("shareNotFound misclassified an error")
	}
}
