package resources

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func icebergTestOptions(mutate func(*databaseIcebergModel)) *databaseIcebergModel {
	model := &databaseIcebergModel{
		Secret:                         types.StringValue("catalog_secret"),
		DefaultSchema:                  types.StringValue("default"),
		Endpoint:                       types.StringNull(),
		Warehouse:                      types.StringNull(),
		EndpointType:                   types.StringNull(),
		ReadOnly:                       types.BoolNull(),
		DefaultRegion:                  types.StringNull(),
		AccessDelegationMode:           types.StringNull(),
		MaxTableStaleness:              types.StringNull(),
		StageCreateTables:              types.BoolNull(),
		SkipCreateTableMetadataUpdates: types.BoolNull(),
		DisableMultiTableCommit:        types.BoolNull(),
		RemoveFilesOnDelete:            types.BoolNull(),
		PurgeRequested:                 types.BoolNull(),
		SupportNestedNamespaces:        types.BoolNull(),
		EncodeEntirePrefix:             types.BoolNull(),
	}
	if mutate != nil {
		mutate(model)
	}
	return model
}

func icebergTestObject(t *testing.T, model *databaseIcebergModel) types.Object {
	t.Helper()
	value, diags := types.ObjectValueFrom(context.Background(), databaseIcebergAttributeTypes(), model)
	if diags.HasError() {
		t.Fatalf("ObjectValueFrom: %v", diags)
	}
	return value
}

func TestDatabaseIcebergCreateOptions(t *testing.T) {
	options := icebergTestOptions(func(m *databaseIcebergModel) {
		m.Secret = types.StringValue("it's_secret")
		m.EndpointType = types.StringValue("s3_tables")
		m.Warehouse = types.StringValue("arn:aws:s3tables:us-east-1:123456789012:bucket/lake")
		m.ReadOnly = types.BoolValue(true)
		m.MaxTableStaleness = types.StringValue("10 minutes")
		m.RemoveFilesOnDelete = types.BoolValue(false)
		m.EncodeEntirePrefix = types.BoolValue(false)
		m.SupportNestedNamespaces = types.BoolValue(true)
	})
	got := databaseIcebergCreateOptions(options)
	want := []string{
		`"secret" 'it''s_secret'`,
		`DEFAULT_SCHEMA 'default'`,
		`WAREHOUSE 'arn:aws:s3tables:us-east-1:123456789012:bucket/lake'`,
		`ENDPOINT_TYPE 's3_tables'`,
		`MAX_TABLE_STALENESS '10 minutes'`,
		`READ_ONLY TRUE`,
		`REMOVE_FILES_ON_DELETE FALSE`,
		`SUPPORT_NESTED_NAMESPACES TRUE`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("create options = %q, want %q", got, want)
	}
	if got := databaseIcebergCreateOptions(icebergTestOptions(func(m *databaseIcebergModel) { m.EncodeEntirePrefix = types.BoolValue(true) })); got[len(got)-1] != "ENCODE_ENTIRE_PREFIX TRUE" {
		t.Fatalf("encode_entire_prefix = true should be emitted, got %q", got)
	}
	if got := databaseIcebergCreateOptions(nil); len(got) != 0 {
		t.Fatalf("nil options rendered %q", got)
	}
}

func TestDatabaseIcebergAlterAssignments(t *testing.T) {
	prior := icebergTestOptions(func(m *databaseIcebergModel) {
		m.Endpoint = types.StringValue("https://catalog.example.com")
		m.DefaultRegion = types.StringValue("eu-central-1")
		m.StageCreateTables = types.BoolValue(true)
		m.EncodeEntirePrefix = types.BoolValue(true)
	})
	tests := map[string]struct {
		planned *databaseIcebergModel
		prior   *databaseIcebergModel
		want    []string
	}{
		"unchanged": {
			planned: icebergTestOptions(func(m *databaseIcebergModel) {
				*m = *prior
			}),
			prior: prior,
			want:  []string{},
		},
		"rotate and clear": {
			planned: icebergTestOptions(func(m *databaseIcebergModel) {
				m.Secret = types.StringValue("rotated")
				m.Endpoint = types.StringValue("https://other.example.com")
				m.StageCreateTables = types.BoolValue(false)
				m.EncodeEntirePrefix = types.BoolValue(false)
			}),
			prior: prior,
			want: []string{
				`"secret" = 'rotated'`,
				`DEFAULT_REGION = NULL`,
				`STAGE_CREATE_TABLES = 'false'`,
				`ENCODE_ENTIRE_PREFIX = NULL`,
			},
		},
		"removing a bool clears it": {
			planned: icebergTestOptions(func(m *databaseIcebergModel) {
				m.Endpoint = prior.Endpoint
				m.DefaultRegion = prior.DefaultRegion
				m.EncodeEntirePrefix = prior.EncodeEntirePrefix
			}),
			prior: prior,
			want:  []string{`STAGE_CREATE_TABLES = NULL`},
		},
		"encode_entire_prefix false to null is a no-op": {
			planned: icebergTestOptions(nil),
			prior:   icebergTestOptions(func(m *databaseIcebergModel) { m.EncodeEntirePrefix = types.BoolValue(false) }),
			want:    []string{},
		},
		"adopting after import applies configured mutable options only": {
			planned: icebergTestOptions(func(m *databaseIcebergModel) {
				m.Warehouse = types.StringValue("lake")
				m.PurgeRequested = types.BoolValue(false)
				m.EncodeEntirePrefix = types.BoolValue(true)
			}),
			prior: nil,
			want: []string{
				`"secret" = 'catalog_secret'`,
				`DEFAULT_SCHEMA = 'default'`,
				`PURGE_REQUESTED = 'false'`,
				`ENCODE_ENTIRE_PREFIX = 'true'`,
			},
		},
		"unmanaged": {
			planned: nil,
			prior:   prior,
			want:    nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := databaseIcebergAlterAssignments(tc.planned, tc.prior)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("assignments = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateDatabaseIcebergConfig(t *testing.T) {
	valid := func(mutate func(*databaseModel)) databaseModel {
		model := databaseModel{
			DatabaseType:          types.StringValue("iceberg"),
			Transient:             types.BoolNull(),
			SnapshotRetentionDays: types.Int64Null(),
			DataPath:              types.StringNull(),
			Encrypted:             types.BoolNull(),
			Iceberg:               icebergTestObject(t, icebergTestOptions(nil)),
		}
		if mutate != nil {
			mutate(&model)
		}
		return model
	}
	tests := map[string]struct {
		model databaseModel
		path  path.Path
	}{
		"valid": {model: valid(nil)},
		"missing block": {
			model: valid(func(m *databaseModel) { m.Iceberg = types.ObjectNull(databaseIcebergAttributeTypes()) }),
			path:  path.Root("iceberg"),
		},
		"block without iceberg type": {
			model: valid(func(m *databaseModel) { m.DatabaseType = types.StringNull() }),
			path:  path.Root("iceberg"),
		},
		"block on ducklake": {
			model: valid(func(m *databaseModel) { m.DatabaseType = types.StringValue("ducklake") }),
			path:  path.Root("iceberg"),
		},
		"transient": {
			model: valid(func(m *databaseModel) { m.Transient = types.BoolValue(true) }),
			path:  path.Root("transient"),
		},
		"snapshot retention": {
			model: valid(func(m *databaseModel) { m.SnapshotRetentionDays = types.Int64Value(7) }),
			path:  path.Root("snapshot_retention_days"),
		},
		"data path": {
			model: valid(func(m *databaseModel) { m.DataPath = types.StringValue("s3://bucket/lake") }),
			path:  path.Root("data_path"),
		},
		"blank secret": {
			model: valid(func(m *databaseModel) {
				m.Iceberg = icebergTestObject(t, icebergTestOptions(func(o *databaseIcebergModel) { o.Secret = types.StringValue(" ") }))
			}),
			path: path.Root("iceberg").AtName("secret"),
		},
		"unknown endpoint type": {
			model: valid(func(m *databaseModel) {
				m.Iceberg = icebergTestObject(t, icebergTestOptions(func(o *databaseIcebergModel) { o.EndpointType = types.StringValue("S3_TABLES") }))
			}),
			path: path.Root("iceberg").AtName("endpoint_type"),
		},
		"unknown access delegation mode": {
			model: valid(func(m *databaseModel) {
				m.Iceberg = icebergTestObject(t, icebergTestOptions(func(o *databaseIcebergModel) { o.AccessDelegationMode = types.StringValue("vended") }))
			}),
			path: path.Root("iceberg").AtName("access_delegation_mode"),
		},
		"unknown database type defers": {
			model: valid(func(m *databaseModel) {
				m.DatabaseType = types.StringUnknown()
				m.Transient = types.BoolValue(true)
			}),
		},
		"unknown block defers": {
			model: valid(func(m *databaseModel) { m.Iceberg = types.ObjectUnknown(databaseIcebergAttributeTypes()) }),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateDatabaseConfig(context.Background(), tc.model, &diags)
			if len(tc.path.Steps()) == 0 {
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			found := false
			for _, d := range diags.Errors() {
				if withPath, ok := d.(diag.DiagnosticWithPath); ok && withPath.Path().Equal(tc.path) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected error at %s, got %v", tc.path, diags)
			}
		})
	}
}

func TestValidateDatabaseConfigIgnoresIcebergForExistingTypes(t *testing.T) {
	for _, databaseType := range []types.String{types.StringNull(), types.StringValue("default"), types.StringValue("ducklake")} {
		model := databaseModel{
			DatabaseType:          databaseType,
			Transient:             types.BoolNull(),
			SnapshotRetentionDays: types.Int64Value(7),
			DataPath:              types.StringNull(),
			Encrypted:             types.BoolNull(),
			Iceberg:               types.ObjectNull(databaseIcebergAttributeTypes()),
		}
		var diags diag.Diagnostics
		validateDatabaseConfig(context.Background(), model, &diags)
		if diags.HasError() {
			t.Fatalf("database_type %s without iceberg: %v", databaseType, diags)
		}
	}
}

// The iceberg block must stay Optional and not Computed, so databases that
// never set it plan a null value and see no diff after an upgrade.
func TestDatabaseIcebergSchemaIsConfigOwned(t *testing.T) {
	var resp resource.SchemaResponse
	NewDatabaseResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	block, ok := resp.Schema.Attributes["iceberg"].(resourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("iceberg attribute = %T, want SingleNestedAttribute", resp.Schema.Attributes["iceberg"])
	}
	if !block.Optional || block.Computed || block.Sensitive {
		t.Fatalf("iceberg block optional=%t computed=%t sensitive=%t, want optional only", block.Optional, block.Computed, block.Sensitive)
	}
	if got, want := len(block.Attributes), len(databaseIcebergAttributeTypes()); got != want {
		t.Fatalf("iceberg block has %d attributes, option table has %d", got, want)
	}
	immutable := map[string]bool{}
	for _, option := range databaseIcebergStringOptions {
		immutable[option.attribute] = option.immutable
	}
	for _, option := range databaseIcebergBoolOptions {
		immutable[option.attribute] = option.immutable
	}
	for name, attribute := range block.Attributes {
		if attribute.IsComputed() {
			t.Errorf("iceberg.%s must not be computed", name)
		}
		var modifiers int
		switch typed := attribute.(type) {
		case resourceschema.StringAttribute:
			modifiers = len(typed.PlanModifiers)
		case resourceschema.BoolAttribute:
			modifiers = len(typed.PlanModifiers)
		default:
			t.Fatalf("iceberg.%s has unexpected type %T", name, attribute)
		}
		if immutable[name] != (modifiers > 0) {
			t.Errorf("iceberg.%s immutable=%t but has %d plan modifiers", name, immutable[name], modifiers)
		}
	}
}

func TestDatabaseTypeValidatorAcceptsIceberg(t *testing.T) {
	for value, wantError := range map[string]bool{"default": false, "ducklake": false, "iceberg": false, "ICEBERG": true, "delta": true} {
		var resp validator.StringResponse
		databaseTypeValidator{}.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("database_type"), ConfigValue: types.StringValue(value)}, &resp)
		if resp.Diagnostics.HasError() != wantError {
			t.Errorf("database_type %q error=%t, want %t: %v", value, resp.Diagnostics.HasError(), wantError, resp.Diagnostics)
		}
		if wantError && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "iceberg") {
			t.Errorf("database_type %q error should list iceberg: %v", value, resp.Diagnostics)
		}
	}
}
