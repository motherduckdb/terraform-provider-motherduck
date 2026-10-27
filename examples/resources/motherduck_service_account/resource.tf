# Destroying a service account deletes the databases it owns. Add
# lifecycle { prevent_destroy = true } to accounts that own production data.
resource "motherduck_service_account" "app" {
  username = "analytics_app"
}
