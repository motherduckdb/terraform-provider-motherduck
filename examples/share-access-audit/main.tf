locals {
  share_names = sort(keys(var.expected_grants))
}

data "motherduck_share_grants" "audit" {
  count      = length(local.share_names)
  share_name = local.share_names[count.index]
}

locals {
  actual_grants = {
    for index, share in data.motherduck_share_grants.audit :
    local.share_names[index] => toset([
      for grant in share.rows : "${grant.grantee_type}:${grant.grantee_name}"
    ])
  }

  audit = {
    for share_name, expected in var.expected_grants :
    share_name => {
      missing    = sort(tolist(setsubtract(expected, local.actual_grants[share_name])))
      unexpected = sort(tolist(setsubtract(local.actual_grants[share_name], expected)))
    }
  }

  compliant = alltrue([
    for result in values(local.audit) :
    length(result.missing) == 0 && length(result.unexpected) == 0
  ])
}
