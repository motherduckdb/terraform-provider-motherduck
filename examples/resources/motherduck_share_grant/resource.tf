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

resource "motherduck_share" "analytics" {
  name            = "analytics_share"
  source_database = motherduck_database.analytics.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "manual"
  include_pattern = ["main.orders"]

  depends_on = [motherduck_table.orders]
}

resource "motherduck_role" "analytics_readers" {
  name = "analytics_readers"
}

resource "motherduck_share_grant" "reader" {
  share        = motherduck_share.analytics.name
  username     = motherduck_role.analytics_readers.name
  grantee_type = "role"
}
