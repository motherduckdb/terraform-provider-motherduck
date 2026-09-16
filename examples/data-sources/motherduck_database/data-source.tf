# The warehouse is managed by another Terraform state or deployment tool.
data "motherduck_database" "warehouse" {
  name = "analytics"
}

output "warehouse_uuid" {
  value = data.motherduck_database.warehouse.uuid
}
