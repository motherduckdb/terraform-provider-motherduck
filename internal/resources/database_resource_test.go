package resources

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestDatabaseSnapshotRetentionUsesStateForUnknown(t *testing.T) {
	var resp resource.SchemaResponse
	NewDatabaseResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["snapshot_retention_days"].(resourceschema.Int64Attribute)
	if !ok {
		t.Fatalf("snapshot_retention_days attribute = %T, want schema.Int64Attribute", resp.Schema.Attributes["snapshot_retention_days"])
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatal("snapshot_retention_days should keep prior state for unknown Optional+Computed plans")
	}
}

func TestValidateDatabaseConfigDefersUnknownDatabaseType(t *testing.T) {
	model := databaseModel{
		DatabaseType: types.StringUnknown(),
		DataPath:     types.StringValue("s3://example-bucket/ducklake"),
		Encrypted:    types.BoolValue(true),
		Transient:    types.BoolValue(true),
	}
	var diags diag.Diagnostics
	validateDatabaseConfig(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("unknown database_type should defer cross-field validation: %v", diags)
	}
}

func TestDatabaseCreateStatementDataInliningRowLimit(t *testing.T) {
	tests := map[string]struct {
		model databaseModel
		want  string
	}{
		"ducklake with limit": {
			model: databaseModel{
				Name:                  types.StringValue("lake"),
				DatabaseType:          types.StringValue("ducklake"),
				Encrypted:             types.BoolValue(true),
				DataInliningRowLimit:  types.Int64Value(25),
				SnapshotRetentionDays: types.Int64Value(7),
			},
			want: `CREATE DATABASE "lake" (TYPE DUCKLAKE, ENCRYPTED, DATA_INLINING_ROW_LIMIT 25, SNAPSHOT_RETENTION_DAYS 7)`,
		},
		"zero disables inlining": {
			model: databaseModel{
				Name:                 types.StringValue("lake"),
				DatabaseType:         types.StringValue("ducklake"),
				DataInliningRowLimit: types.Int64Value(0),
			},
			want: `CREATE DATABASE "lake" (TYPE DUCKLAKE, DATA_INLINING_ROW_LIMIT 0)`,
		},
		"omitted limit": {
			model: databaseModel{
				Name:                 types.StringValue("lake"),
				DatabaseType:         types.StringValue("ducklake"),
				DataInliningRowLimit: types.Int64Null(),
			},
			want: `CREATE DATABASE "lake" (TYPE DUCKLAKE)`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := databaseCreateStatement(context.Background(), tc.model, &diags)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			if got != tc.want {
				t.Fatalf("statement = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateDatabaseConfigDataInliningRowLimitRequiresDuckLake(t *testing.T) {
	tests := map[string]struct {
		databaseType types.String
		wantErr      bool
	}{
		"ducklake": {databaseType: types.StringValue("ducklake"), wantErr: false},
		"default":  {databaseType: types.StringValue("default"), wantErr: true},
		"omitted":  {databaseType: types.StringNull(), wantErr: true},
		"iceberg":  {databaseType: types.StringValue("iceberg"), wantErr: true},
		"unknown":  {databaseType: types.StringUnknown(), wantErr: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			model := databaseModel{
				DatabaseType:         tc.databaseType,
				DataInliningRowLimit: types.Int64Value(10),
			}
			var diags diag.Diagnostics
			validateDatabaseConfig(context.Background(), model, &diags)
			found := false
			for _, d := range diags.Errors() {
				if strings.Contains(d.Detail(), "data_inlining_row_limit") {
					found = true
				}
			}
			if found != tc.wantErr {
				t.Fatalf("data_inlining_row_limit error = %t, want %t: %v", found, tc.wantErr, diags)
			}
		})
	}
}

func TestDatabaseDataInliningRowLimitIsCreateOnly(t *testing.T) {
	var resp resource.SchemaResponse
	NewDatabaseResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attr, ok := resp.Schema.Attributes["data_inlining_row_limit"].(resourceschema.Int64Attribute)
	if !ok {
		t.Fatalf("data_inlining_row_limit attribute = %T, want schema.Int64Attribute", resp.Schema.Attributes["data_inlining_row_limit"])
	}
	if !attr.Optional || attr.Computed || len(attr.PlanModifiers) != 1 || len(attr.Validators) != 1 {
		t.Fatalf("data_inlining_row_limit = %#v, want optional, not computed, with a replace modifier and a minimum validator", attr)
	}
	var vresp validator.Int64Response
	attr.Validators[0].ValidateInt64(context.Background(), validator.Int64Request{Path: path.Root("data_inlining_row_limit"), ConfigValue: types.Int64Value(-1)}, &vresp)
	if !vresp.Diagnostics.HasError() {
		t.Fatal("negative data_inlining_row_limit should be rejected")
	}
}

func TestIntervalDays(t *testing.T) {
	tests := map[string]types.Int64{
		"7 days":     types.Int64Value(7),
		"00:00:00":   types.Int64Value(0),
		"unparsable": types.Int64Null(),
	}
	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			got := intervalDays(value)
			if !got.Equal(want) {
				t.Fatalf("intervalDays() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestApplyDatabaseRow(t *testing.T) {
	tests := map[string]struct {
		model         databaseModel
		uuid          sql.NullString
		createdTS     sql.NullString
		dbType        sql.NullString
		transient     sql.NullBool
		retention     sql.NullString
		wantTransient types.Bool
		wantRetention types.Int64
		wantType      types.String
	}{
		"null transient and retention become known nulls": {
			model: databaseModel{
				Name:                  types.StringValue("ducklake_db"),
				Transient:             types.BoolUnknown(),
				SnapshotRetentionDays: types.Int64Unknown(),
			},
			uuid:          sqlNullString("uuid"),
			createdTS:     sqlNullString("2026-01-01 00:00:00"),
			dbType:        sqlNullString("DUCKLAKE"),
			transient:     sql.NullBool{},
			retention:     sqlNullStringInvalid(),
			wantTransient: types.BoolNull(),
			wantRetention: types.Int64Null(),
			wantType:      types.StringValue("ducklake"),
		},
		"stale state values are overwritten by null live values": {
			model: databaseModel{
				Name:                  types.StringValue("db"),
				Transient:             types.BoolValue(true),
				SnapshotRetentionDays: types.Int64Value(5),
			},
			uuid:          sqlNullString("uuid"),
			createdTS:     sqlNullString("2026-01-01 00:00:00"),
			dbType:        sqlNullString("DEFAULT"),
			transient:     sql.NullBool{},
			retention:     sqlNullStringInvalid(),
			wantTransient: types.BoolNull(),
			wantRetention: types.Int64Null(),
			wantType:      types.StringValue("default"),
		},
		"valid values are mapped": {
			model: databaseModel{
				Name: types.StringValue("db"),
			},
			uuid:          sqlNullString("uuid"),
			createdTS:     sqlNullString("2026-01-01 00:00:00"),
			dbType:        sqlNullString("DEFAULT"),
			transient:     sql.NullBool{Bool: true, Valid: true},
			retention:     sqlNullString("7 days"),
			wantTransient: types.BoolValue(true),
			wantRetention: types.Int64Value(7),
			wantType:      types.StringValue("default"),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			applyDatabaseRow(&tc.model, tc.uuid, tc.createdTS, tc.dbType, tc.transient, tc.retention)
			if got, want := tc.model.ID, types.StringValue(tc.model.Name.ValueString()); !got.Equal(want) {
				t.Fatalf("id = %#v, want %#v", got, want)
			}
			if !tc.model.Transient.Equal(tc.wantTransient) {
				t.Fatalf("transient = %#v, want %#v", tc.model.Transient, tc.wantTransient)
			}
			if !tc.model.SnapshotRetentionDays.Equal(tc.wantRetention) {
				t.Fatalf("snapshot_retention_days = %#v, want %#v", tc.model.SnapshotRetentionDays, tc.wantRetention)
			}
			if !tc.model.DatabaseType.Equal(tc.wantType) {
				t.Fatalf("database_type = %#v, want %#v", tc.model.DatabaseType, tc.wantType)
			}
			if got, want := tc.model.UUID, types.StringValue("uuid"); !got.Equal(want) {
				t.Fatalf("uuid = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDatabaseTypeValidator(t *testing.T) {
	ctx := context.Background()
	v := databaseTypeValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"default":   {value: types.StringValue("default"), wantErr: false},
		"case":      {value: types.StringValue("DUCKLAKE"), wantErr: true},
		"transient": {value: types.StringValue("transient"), wantErr: true},
		"blank":     {value: types.StringValue("  "), wantErr: true},
		"unknown":   {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("database_type"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}
