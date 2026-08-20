resource "motherduck_role" "guide_readers" {
  name = "guide_readers"
}

resource "motherduck_guide" "revenue" {
  topic          = "metrics/revenue"
  title          = "Revenue metrics"
  description    = "Canonical revenue definitions and source tables"
  access         = "role"
  role_names     = [motherduck_role.guide_readers.name]
  change_comment = "initial Terraform import"

  content = <<-MARKDOWN
    # Revenue metrics

    Revenue is calculated from finalized invoices.
  MARKDOWN

  references = [
    {
      type        = "catalog"
      url         = "md:analytics"
      schema      = "main"
      table       = "invoices"
      description = "Authoritative invoice source"
    }
  ]
}
