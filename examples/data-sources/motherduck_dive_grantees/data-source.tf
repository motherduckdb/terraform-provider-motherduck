data "motherduck_dive_grantees" "revenue" {
  dive_id = "123e4567-e89b-42d3-a456-426614174000"
}

output "revenue_dive_is_shared_with_the_organization" {
  value = anytrue([for row in data.motherduck_dive_grantees.revenue.rows : row.grantee_type == "organization"])
}
