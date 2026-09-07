resource "motherduck_database" "analytics" {
  name = "analytics"
}

resource "motherduck_table" "invoices" {
  database = motherduck_database.analytics.name
  schema   = "main"
  name     = "invoices"
  columns = {
    invoice_id = "INTEGER"
    amount     = "DOUBLE"
  }
}

resource "motherduck_guide" "revenue" {
  topic          = "metrics/revenue"
  title          = "Revenue metrics"
  description    = "Canonical revenue definitions and source tables"
  access         = "user"
  change_comment = "initial version"

  content = <<-MARKDOWN
    # Revenue metrics

    Revenue is calculated from finalized invoices.
  MARKDOWN

  references = [
    {
      type        = "catalog"
      url         = "md:${motherduck_database.analytics.name}"
      schema      = motherduck_table.invoices.schema
      table       = motherduck_table.invoices.name
      description = "Authoritative invoice source"
    }
  ]
}
