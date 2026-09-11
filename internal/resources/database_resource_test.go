package resources

import (
	"context"
	"database/sql"
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
	validateDatabaseConfig(model, &diags)
	if diags.HasError() {
		t.Fatalf("unknown database_type should defer cross-field validation: %v", diags)
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
