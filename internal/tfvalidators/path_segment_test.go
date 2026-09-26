package tfvalidators

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRESTPathSegment(t *testing.T) {
	for _, tc := range []struct {
		value   types.String
		wantErr bool
	}{
		{types.StringValue("."), true},
		{types.StringValue(".."), true},
		{types.StringValue("svc.reader"), false},
		{types.StringValue("..svc"), false},
		{types.StringNull(), false},
		{types.StringUnknown(), false},
	} {
		var resp validator.StringResponse
		RESTPathSegment("username").ValidateString(t.Context(), validator.StringRequest{Path: path.Root("username"), ConfigValue: tc.value}, &resp)
		if resp.Diagnostics.HasError() != tc.wantErr {
			t.Fatalf("RESTPathSegment(%s) error = %v, want %v", tc.value, resp.Diagnostics, tc.wantErr)
		}
	}
}
