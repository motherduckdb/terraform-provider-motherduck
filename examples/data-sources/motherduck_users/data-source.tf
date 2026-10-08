data "motherduck_users" "service_accounts" {
  is_service_account = true
  is_deprovisioned   = false
}

output "service_account_usernames" {
  value = [for user in data.motherduck_users.service_accounts.users : user.username]
}
