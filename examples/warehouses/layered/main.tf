resource "motherduck_database" "layer" {
  for_each                = toset(["raw", "transform", "marts"])
  name                    = "${var.name_prefix}_${var.environment}_${each.key}"
  snapshot_retention_days = each.key == "raw" ? 14 : 7
}

resource "motherduck_table" "orders" {
  database = motherduck_database.layer["raw"].name
  schema   = "main"
  name     = "orders"
  columns = {
    order_id        = "VARCHAR"
    order_date      = "DATE"
    amount          = "DECIMAL(18,2)"
    status          = "VARCHAR"
    source_revision = "BIGINT"
  }
}

resource "motherduck_view" "orders_latest" {
  database = motherduck_database.layer["transform"].name
  schema   = "main"
  name     = "orders_latest"
  query = templatefile("${path.module}/orders_latest.sql.tftpl", {
    raw_database = motherduck_database.layer["raw"].name
  })
  depends_on = [motherduck_table.orders]
}

resource "motherduck_table" "daily_revenue" {
  database = motherduck_database.layer["marts"].name
  schema   = "main"
  name     = "daily_revenue"
  columns = {
    order_date  = "DATE"
    order_count = "BIGINT"
    revenue     = "DECIMAL(18,2)"
  }
}
