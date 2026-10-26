data "motherduck_dives" "recent" {
  limit              = 10
  include_org_shares = true
}
