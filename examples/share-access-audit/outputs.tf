output "audit" {
  description = "Sorted missing and unexpected audiences for each audited share."
  value       = local.audit
}

output "compliant" {
  description = "Whether every audited share matches its expected audiences."
  value       = local.compliant

  precondition {
    condition     = !var.fail_on_drift || local.compliant
    error_message = "Share access drift detected. Rerun with fail_on_drift=false for a report."
  }
}
