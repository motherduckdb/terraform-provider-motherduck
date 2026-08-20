resource "motherduck_database" "analytics" {
  name = "analytics"
}

resource "motherduck_share" "analytics" {
  name            = "analytics_share"
  source_database = motherduck_database.analytics.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "manual"
  include_pattern = ["main.reporting_*", "main.dim_*"]
}

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.analytics.name
  username = "reader_user"
}
