resource "motherduck_database" "analytics" {
  name                    = "analytics"
  snapshot_retention_days = 7
}

# Register an existing Iceberg REST catalog as a MotherDuck database. The
# catalog credentials live in a MotherDuck secret, for example a
# motherduck_secret with type = "iceberg", and the default_schema namespace
# must already exist in the catalog.
resource "motherduck_database" "lakehouse" {
  name          = "lakehouse"
  database_type = "iceberg"

  iceberg = {
    secret         = "lakehouse_catalog"
    endpoint       = "https://catalog.example.com"
    warehouse      = "analytics_warehouse"
    default_schema = "default"
  }
}
