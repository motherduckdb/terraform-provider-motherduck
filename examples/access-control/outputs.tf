output "role_names" {
  description = "Role name created for each team."
  value       = { for team_key, role in motherduck_role.team : team_key => role.name }
}

output "direct_grants" {
  description = "Grants managed by this configuration, sorted for comparison."
  value = sort(concat(
    [for key, grant in motherduck_role_grant.platform : "role:${grant.grantee_name} inherits ${grant.role_name}"],
    [for key, grant in motherduck_role_grant.member : "user:${grant.grantee_name} holds ${grant.role_name}"],
    [for key, grant in motherduck_share_grant.team : "role:${grant.username} reads share ${grant.share}"],
  ))
}
