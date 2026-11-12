resource "motherduck_dive" "revenue" {
  title   = "Revenue"
  content = <<-JSX
    export default function Dive() {
      return <h1>Revenue</h1>
    }
  JSX
}

resource "motherduck_role" "finance" {
  name = "finance"
}

# Let every member of the finance role open the Dive.
resource "motherduck_dive_grant" "finance" {
  dive_id      = motherduck_dive.revenue.id
  grantee_type = "role"
  grantee_name = motherduck_role.finance.name
}

# Let the service account that creates embed sessions read the Dive.
resource "motherduck_dive_grant" "embed" {
  dive_id      = motherduck_dive.revenue.id
  grantee_name = "embed_service"
}
