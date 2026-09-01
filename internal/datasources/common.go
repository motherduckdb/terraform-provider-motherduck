package datasources

import (
	"context"
	"fmt"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type baseDataSource struct {
	provider *providerctx.Context
}

func (d *baseDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	providerData, ok := req.ProviderData.(*providerctx.Context)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerctx.Context, got %T", req.ProviderData))
		return
	}
	d.provider = providerData
}

func (d *baseDataSource) rest(diags *diag.Diagnostics) *mdrest.Client {
	if d.provider == nil || d.provider.REST == nil || !d.provider.REST.Available() {
		diags.AddError("MotherDuck admin token required", mdrest.ErrMissingAdminToken.Error())
		return nil
	}
	return d.provider.REST
}

func (d *baseDataSource) sql(ctx context.Context, diags *diag.Diagnostics) providerctx.SQLClient {
	if d.provider == nil {
		diags.AddError("MotherDuck token required", mdsql.ErrMissingToken.Error())
		return nil
	}
	client, err := d.provider.SQLClient(ctx)
	if err != nil || client == nil || !client.Available() {
		if err == nil {
			err = mdsql.ErrMissingToken
		}
		diags.AddError("MotherDuck token required", err.Error())
		return nil
	}
	return client
}

func restUsernameValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck REST username", 1, 255)}
}

func uuidValidators() []validator.String {
	return []validator.String{tfvalidators.UUID()}
}
