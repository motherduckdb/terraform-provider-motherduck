resource "motherduck_database" "analytics" {
  name = "analytics"
}

resource "motherduck_table" "orders" {
  database = motherduck_database.analytics.name
  schema   = "main"
  name     = "orders"
  columns = {
    order_id = "VARCHAR"
  }
}

resource "motherduck_view" "daily_revenue" {
  database = motherduck_database.analytics.name
  schema   = "main"
  name     = "daily_revenue"
  query    = "SELECT order_id FROM \"${motherduck_database.analytics.name}\".\"main\".\"${motherduck_table.orders.name}\""
}

resource "motherduck_share" "analytics" {
  name            = "analytics_share"
  source_database = motherduck_database.analytics.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "manual"
  include_pattern = ["main.orders", "main.daily_revenue"]

  # include_pattern names objects by string, so order the share after them.
  depends_on = [motherduck_view.daily_revenue]
}

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.analytics.name
  username = var.reader_username
}

variable "reader_username" {
  description = "Existing grantable MotherDuck user or service-account principal."
  type        = string
  default     = "reader_user"
  nullable    = false
}
