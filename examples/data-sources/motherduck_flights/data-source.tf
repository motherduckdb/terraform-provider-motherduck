data "motherduck_flights" "recent" {
  limit      = 10
  owner_only = true
}
