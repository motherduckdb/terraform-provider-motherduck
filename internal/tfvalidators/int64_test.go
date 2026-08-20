package tfvalidators

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestInt64MinValidator(t *testing.T) {
	ctx := context.Background()
	v := Int64Min("MotherDuck snapshot retention days", 0)

	tests := map[string]struct {
		value   types.Int64
		wantErr bool
	}{
		"zero":     {value: types.Int64Value(0), wantErr: false},
		"positive": {value: types.Int64Value(7), wantErr: false},
		"negative": {value: types.Int64Value(-1), wantErr: true},
		"unknown":  {value: types.Int64Unknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.Int64Response
			v.ValidateInt64(ctx, validator.Int64Request{
				Path:        path.Root("snapshot_retention_days"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestInt64RangeValidator(t *testing.T) {
	v := Int64Range("timeout", 1, 10)
	for name, tc := range map[string]struct {
		value   types.Int64
		wantErr bool
	}{
		"minimum": {value: types.Int64Value(1)},
		"maximum": {value: types.Int64Value(10)},
		"below":   {value: types.Int64Value(0), wantErr: true},
		"above":   {value: types.Int64Value(11), wantErr: true},
		"unknown": {value: types.Int64Unknown()},
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.Int64Response
			v.ValidateInt64(context.Background(), validator.Int64Request{Path: path.Root("timeout"), ConfigValue: tc.value}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
		})
	}
}
