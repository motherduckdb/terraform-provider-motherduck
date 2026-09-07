# The named share must be discoverable. A hidden share is accessed by URL and
# does not appear in this catalog, even when the caller has a direct grant.
data "motherduck_shared_with_me" "analytics" {
  name = "analytics_share"
}
