data "motherduck_flights" "recent" {
  limit      = 10
  offset     = 0
  owner_only = true
}
