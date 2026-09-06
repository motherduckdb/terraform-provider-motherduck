resource "motherduck_database" "warehouse" {
  name                    = "${var.name_prefix}_${var.environment}_simple"
  snapshot_retention_days = 7
}

resource "motherduck_schema" "raw" {
  database = motherduck_database.warehouse.name
  name     = "raw"
}

resource "motherduck_schema" "analytics" {
  database = motherduck_database.warehouse.name
  name     = "analytics"
}

resource "motherduck_table" "orders" {
  database = motherduck_database.warehouse.name
  schema   = motherduck_schema.raw.name
  name     = "orders"
  columns = {
    order_id   = "VARCHAR"
    order_date = "DATE"
    amount     = "DECIMAL(18,2)"
    status     = "VARCHAR"
  }
}

resource "motherduck_view" "daily_revenue" {
  database = motherduck_database.warehouse.name
  schema   = motherduck_schema.analytics.name
  name     = "daily_revenue"
  query = templatefile("${path.module}/daily_revenue.sql.tftpl", {
    database = motherduck_database.warehouse.name
  })
  depends_on = [motherduck_table.orders]
}
