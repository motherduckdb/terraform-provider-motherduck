data "motherduck_flight_grantees" "refresh_crm" {
  flight_id = "123e4567-e89b-42d3-a456-426614174000"
}

output "refresh_crm_runners" {
  value = [for row in data.motherduck_flight_grantees.refresh_crm.rows : "${row.grantee_type}:${row.grantee_name}"]
}
