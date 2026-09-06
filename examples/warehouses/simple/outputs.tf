output "database_name" {
  description = "Database owned by the authenticated environment writer."
  value       = motherduck_database.warehouse.name
}
output "revenue_relation" {
  description = "Daily completed-order revenue: one row per order_date."
  value       = "${motherduck_database.warehouse.name}.analytics.daily_revenue"
}

output "demo_sql" {
  description = "Optional seed SQL for an empty disposable warehouse; never run against production."
  value       = templatefile("${path.module}/demo.sql.tftpl", { database = motherduck_database.warehouse.name })
}
