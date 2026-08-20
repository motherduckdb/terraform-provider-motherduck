package resources

import "github.com/hashicorp/terraform-plugin-framework/resource"

func All() []func() resource.Resource {
	return []func() resource.Resource{
		NewServiceAccountResource,
		NewAccessTokenResource,
		NewDucklingConfigResource,
		NewDatabaseResource,
		NewSchemaResource,
		NewTableResource,
		NewViewResource,
		NewSecretResource,
		NewRoleResource,
		NewRoleGrantResource,
		NewShareResource,
		NewShareGrantResource,
		NewSnapshotResource,
		NewDiveResource,
		NewGuideResource,
		NewFlightResource,
		NewFlightRunResource,
	}
}
