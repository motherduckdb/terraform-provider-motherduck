# Mints a read scaling token for the identity behind admin_token. Secure view
# policies read the context with session_context('tenant').
ephemeral "motherduck_short_lived_token" "acme_reader" {
  token_type = "read_scaling"

  context = {
    tenant = "acme"
    # Routes the token's requests to the read scaling replica named acme.
    name = "acme"
  }
}
