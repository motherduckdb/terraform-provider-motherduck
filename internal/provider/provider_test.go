package provider

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderRegistersResourcesAndDataSources(t *testing.T) {
	p := New("test")()
	gotResources := registeredResourceTypeNames(t, p)
	wantResources := []string{
		"motherduck_access_token",
		"motherduck_database",
		"motherduck_dive",
		"motherduck_duckling_config",
		"motherduck_flight",
		"motherduck_flight_run",
		"motherduck_guide",
		"motherduck_role",
		"motherduck_role_grant",
		"motherduck_schema",
		"motherduck_secret",
		"motherduck_service_account",
		"motherduck_share",
		"motherduck_share_grant",
		"motherduck_snapshot",
		"motherduck_table",
		"motherduck_view",
	}
	if !reflect.DeepEqual(gotResources, wantResources) {
		t.Fatalf("registered resources = %#v, want %#v", gotResources, wantResources)
	}

	gotDataSources := registeredDataSourceTypeNames(t, p)
	wantDataSources := []string{
		"motherduck_active_accounts",
		"motherduck_attached_databases",
		"motherduck_buckets_for_secret",
		"motherduck_current_user",
		"motherduck_database_snapshots",
		"motherduck_databases",
		"motherduck_dive",
		"motherduck_dive_embed_session",
		"motherduck_dive_versions",
		"motherduck_dives",
		"motherduck_files",
		"motherduck_flight",
		"motherduck_flight_logs",
		"motherduck_flight_runs",
		"motherduck_flight_versions",
		"motherduck_flights",
		"motherduck_guide",
		"motherduck_guide_grantees",
		"motherduck_guide_versions",
		"motherduck_guides",
		"motherduck_live_duckling_size",
		"motherduck_owned_share",
		"motherduck_owned_shares",
		"motherduck_role_members",
		"motherduck_roles",
		"motherduck_roles_for_role",
		"motherduck_roles_for_user",
		"motherduck_secrets",
		"motherduck_shared_with_me",
		"motherduck_user_tokens",
		"motherduck_version",
	}
	if !reflect.DeepEqual(gotDataSources, wantDataSources) {
		t.Fatalf("registered data sources = %#v, want %#v", gotDataSources, wantDataSources)
	}

	gotEphemeralResources := registeredEphemeralResourceTypeNames(t, p)
	wantEphemeralResources := []string{
		"motherduck_dive_embed_session",
	}
	if !reflect.DeepEqual(gotEphemeralResources, wantEphemeralResources) {
		t.Fatalf("registered ephemeral resources = %#v, want %#v", gotEphemeralResources, wantEphemeralResources)
	}
}

func registeredResourceTypeNames(t *testing.T, p provider.Provider) []string {
	t.Helper()

	var names []string
	for _, factory := range p.Resources(context.Background()) {
		item := factory()
		var resp resource.MetadataResponse
		item.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "motherduck"}, &resp)
		if resp.TypeName == "" {
			t.Fatalf("registered resource %T returned an empty type name", item)
		}
		names = append(names, resp.TypeName)
	}
	sort.Strings(names)
	return names
}

func registeredDataSourceTypeNames(t *testing.T, p provider.Provider) []string {
	t.Helper()

	var names []string
	for _, factory := range p.DataSources(context.Background()) {
		item := factory()
		var resp datasource.MetadataResponse
		item.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "motherduck"}, &resp)
		if resp.TypeName == "" {
			t.Fatalf("registered data source %T returned an empty type name", item)
		}
		names = append(names, resp.TypeName)
	}
	sort.Strings(names)
	return names
}

func registeredEphemeralResourceTypeNames(t *testing.T, p provider.Provider) []string {
	t.Helper()

	ephemeralProvider, ok := p.(provider.ProviderWithEphemeralResources)
	if !ok {
		t.Fatalf("provider %T does not implement ProviderWithEphemeralResources", p)
	}

	var names []string
	for _, factory := range ephemeralProvider.EphemeralResources(context.Background()) {
		item := factory()
		var resp ephemeral.MetadataResponse
		item.Metadata(context.Background(), ephemeral.MetadataRequest{ProviderTypeName: "motherduck"}, &resp)
		if resp.TypeName == "" {
			t.Fatalf("registered ephemeral resource %T returned an empty type name", item)
		}
		names = append(names, resp.TypeName)
	}
	sort.Strings(names)
	return names
}

func TestAPIBaseURLValidator(t *testing.T) {
	ctx := context.Background()
	v := apiBaseURLValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"defaulted null": {value: types.StringNull(), wantErr: false},
		"https":          {value: types.StringValue("https://api.motherduck.com"), wantErr: false},
		"http local":     {value: types.StringValue("http://localhost:8080/prefix"), wantErr: false},
		"missing scheme": {value: types.StringValue("api.motherduck.com"), wantErr: true},
		"missing host":   {value: types.StringValue("https:///api"), wantErr: true},
		"bad scheme":     {value: types.StringValue("ftp://api.motherduck.com"), wantErr: true},
		"credentials":    {value: types.StringValue("https://user:pass@api.motherduck.com"), wantErr: true},
		"query":          {value: types.StringValue("https://api.motherduck.com?token=bad"), wantErr: true},
		"fragment":       {value: types.StringValue("https://api.motherduck.com#v1"), wantErr: true},
		"whitespace":     {value: types.StringValue(" https://api.motherduck.com"), wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("api_base_url"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestAttachModeValidator(t *testing.T) {
	ctx := context.Background()
	v := attachModeValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"defaulted null": {value: types.StringNull(), wantErr: false},
		"single":         {value: types.StringValue("single"), wantErr: false},
		"workspace":      {value: types.StringValue("workspace"), wantErr: false},
		"empty":          {value: types.StringValue(""), wantErr: false},
		"unsupported":    {value: types.StringValue("isolated"), wantErr: true},
		"case":           {value: types.StringValue("SINGLE"), wantErr: true},
		"whitespace":     {value: types.StringValue(" single"), wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("attach_mode"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestConfigureStoresLazySQLConfig(t *testing.T) {
	t.Setenv("MOTHERDUCK_TOKEN", "md_test_token")
	t.Setenv("MOTHERDUCK_ADMIN_TOKEN", "")

	p := New("test")()
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), provider.ConfigureRequest{Config: providerTestConfig(t, map[string]tftypes.Value{})}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	data, ok := resp.ResourceData.(*providerctx.Context)
	if !ok {
		t.Fatalf("ResourceData = %T, want *providerctx.Context", resp.ResourceData)
	}
	if data.SQL != nil {
		t.Fatal("Configure should not eagerly initialize the SQL client")
	}
	if data.SQLConfig.Token != "md_test_token" {
		t.Fatalf("SQLConfig.Token = %q, want env token", data.SQLConfig.Token)
	}
	if resp.EphemeralResourceData != resp.ResourceData || resp.DataSourceData != resp.ResourceData {
		t.Fatal("provider data should be shared across resources, data sources, and ephemeral resources")
	}
}

func TestConfigureSingleAttachRequiresDatabase(t *testing.T) {
	p := New("test")()
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), provider.ConfigureRequest{Config: providerTestConfig(t, map[string]tftypes.Value{
		"attach_mode": tftypes.NewValue(tftypes.String, "single"),
	})}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected configure diagnostics")
	}
	if got := resp.Diagnostics[0].Summary(); got != "MotherDuck database required" {
		t.Fatalf("diagnostic summary = %q, want database requirement", got)
	}
}

func providerTestConfig(t *testing.T, values map[string]tftypes.Value) tfsdk.Config {
	t.Helper()
	p := New("test")()
	var schemaResp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	attrTypes := map[string]tftypes.Type{
		"token":                   tftypes.String,
		"admin_token":             tftypes.String,
		"api_base_url":            tftypes.String,
		"database":                tftypes.String,
		"attach_mode":             tftypes.String,
		"custom_user_agent":       tftypes.String,
		"request_timeout_seconds": tftypes.Number,
	}
	rawValues := map[string]tftypes.Value{}
	for name, attrType := range attrTypes {
		rawValues[name] = tftypes.NewValue(attrType, nil)
	}
	for name, value := range values {
		rawValues[name] = value
	}
	return tfsdk.Config{
		Raw:    tftypes.NewValue(tftypes.Object{AttributeTypes: attrTypes}, rawValues),
		Schema: schemaResp.Schema,
	}
}
