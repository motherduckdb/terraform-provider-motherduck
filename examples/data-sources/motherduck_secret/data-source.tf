data "motherduck_secret" "catalog" {
  name = "lakehouse_catalog"
}

resource "motherduck_database" "lakehouse" {
  name          = "lakehouse"
  database_type = "iceberg"

  iceberg = {
    secret         = data.motherduck_secret.catalog.name
    endpoint       = "https://catalog.example.com"
    warehouse      = "analytics_warehouse"
    default_schema = "default"
  }
}
