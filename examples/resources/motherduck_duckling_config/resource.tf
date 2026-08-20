resource "motherduck_duckling_config" "app" {
  username = "analytics_app"

  read_write_instance_size      = "standard"
  read_write_cooldown_seconds   = 600
  read_scaling_instance_size    = "standard"
  read_scaling_flock_size       = 2
  read_scaling_cooldown_seconds = 600
}

