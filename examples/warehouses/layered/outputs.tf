output "databases" {
  description = "Raw/transform/marts database names, all owned by this environment's writer."
  value       = { for layer, database in motherduck_database.layer : layer => database.name }
}
output "refresh_sql" {
  description = "Writer pipeline SQL to refresh the marts table without changing its schema."
  value = templatefile("${path.module}/refresh_marts.sql.tftpl", {
    marts_database     = motherduck_database.layer["marts"].name
    transform_database = motherduck_database.layer["transform"].name
  })
}
output "demo_sql" {
  description = "Optional seed SQL for an empty disposable warehouse. Never run against production."
  value       = templatefile("${path.module}/demo.sql.tftpl", { raw_database = motherduck_database.layer["raw"].name })
}
