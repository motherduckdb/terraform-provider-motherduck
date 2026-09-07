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
  database   = motherduck_database.analytics.name
  schema     = "main"
  name       = "daily_revenue"
  query      = "SELECT order_id FROM \"${motherduck_database.analytics.name}\".\"main\".\"${motherduck_table.orders.name}\""
  depends_on = [motherduck_table.orders]
}

resource "motherduck_share" "analytics" {
  name            = "analytics_share"
  source_database = motherduck_database.analytics.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "manual"
  include_pattern = ["main.orders", "main.daily_revenue"]
  depends_on      = [motherduck_view.daily_revenue]
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
  validation {
    condition     = length(trimspace(var.reader_username)) > 0 && trimspace(var.reader_username) == var.reader_username
    error_message = "reader_username must be a non-empty principal without leading or trailing whitespace."
  }
}
