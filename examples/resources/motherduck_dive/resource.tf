resource "motherduck_database" "wikipedia" {
  name = "wikipedia_pageviews"
}

resource "motherduck_share" "wikipedia" {
  name            = "wikipedia_pageviews"
  source_database = motherduck_database.wikipedia.name
  access          = "unrestricted"
  visibility      = "hidden"
  update_mode     = "automatic"
}

resource "motherduck_dive" "revenue" {
  title       = "Revenue"
  description = "Revenue overview"
  api_version = 1
  status      = "ready"

  required_resources = [
    {
      alias = "wikipedia_pageviews"
      url   = motherduck_share.wikipedia.url
    }
  ]

  content = <<-JSX
    export default function Dive() {
      return <div>Revenue</div>;
    }
  JSX
}
