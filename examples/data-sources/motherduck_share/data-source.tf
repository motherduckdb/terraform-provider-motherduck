# Describe a share owned by the current account by name.
data "motherduck_share" "owned" {
  name = "wikipedia_pageviews"
}

# Describe a share received from another account by its URL. Keep the URL in
# a sensitive variable because unrestricted share URLs can grant access.
variable "received_share_url" {
  type      = string
  sensitive = true
}

data "motherduck_share" "received" {
  url = var.received_share_url
}

output "received_share_source_database" {
  value = data.motherduck_share.received.source_database
}
