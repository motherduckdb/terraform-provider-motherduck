output "database" {
  description = "DuckLake database name."
  value       = motherduck_database.lake.name
}

output "table" {
  description = "Fully qualified landing table for the pipeline."
  value       = "${motherduck_database.lake.name}.${motherduck_schema.events.name}.${motherduck_table.page_views.name}"
}

output "secret_name" {
  description = "MotherDuck secret that grants access to the lake prefix."
  value       = motherduck_secret.lake.name
}
