data "motherduck_share_secure_views" "usage" {
  share_name = "usage_share"
}

output "secure_view_names" {
  value = [
    for view in data.motherduck_share_secure_views.usage.rows :
    "${view.schema_name}.${view.view_name}"
  ]
}
