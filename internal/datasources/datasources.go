package datasources

import "github.com/hashicorp/terraform-plugin-framework/datasource"

func All() []func() datasource.DataSource {
	sources := []func() datasource.DataSource{
		NewActiveAccountsDataSource,
		NewUserTokensDataSource,
		NewDiveEmbedSessionDataSource,
		NewOwnedShareDataSource,
		NewCurrentUserDataSource,
		NewVersionDataSource,
		NewLiveDucklingSizeDataSource,
	}
	for _, spec := range rowSpecs() {
		spec := spec
		sources = append(sources, func() datasource.DataSource { return &rowsDataSource{spec: spec} })
	}
	return sources
}
