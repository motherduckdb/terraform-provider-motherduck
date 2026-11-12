resource "motherduck_flight" "refresh_crm" {
  name            = "refresh_crm"
  max_runtime_sec = 900

  source_code = <<-PY
    def main():
        print("refreshing")

    if __name__ == "__main__":
        main()
  PY
}

resource "motherduck_role" "operators" {
  name = "operators"
}

# Let every member of the operators role start runs of the Flight.
resource "motherduck_flight_grant" "operators" {
  flight_id    = motherduck_flight.refresh_crm.id
  grantee_type = "role"
  grantee_name = motherduck_role.operators.name
}
