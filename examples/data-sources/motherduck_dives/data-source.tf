data "motherduck_dives" "recent" {
  limit              = 10
  offset             = 0
  include_org_shares = true
}
