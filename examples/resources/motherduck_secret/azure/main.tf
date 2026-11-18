resource "motherduck_secret" "azure" {
  name = "analytics_azure"
  type = "azure"

  params = {
    connection_string = var.azure_storage_connection_string
    scope             = "azure://analytics-container/"
  }
}

variable "azure_storage_connection_string" {
  type      = string
  sensitive = true
}
