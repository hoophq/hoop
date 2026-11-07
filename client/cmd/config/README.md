# Hoop config profiles

`~/.hoop/config.toml` holds every account. Each account is one `[profile <name>]` section. `current` selects the account. When `current` is absent, the first profile is active.

## Commands

| Command | Action |
| --- | --- |
| `hoop config get-contexts` | List profiles. The active profile is marked `*`. Aliases: `contexts`, `ls`. |
| `hoop config current-context` | Print the active profile name. |
| `hoop config use-context <name>` | Select a profile. The other profiles stay in the file. Alias: `use`. |
| `hoop config set-context <name>` | Save the active credentials under `<name>` and select that profile. |
| `hoop config delete-context <name>` | Delete a profile. The active profile cannot be deleted. |
| `hoop config view` | Show the active profile as a flat config. |
| `hoop config create` | Write the API URL, gRPC URL, and token of the active profile. |
| `hoop config clear` | Delete `config.toml`. |

`hoop login` updates the token of the active profile. The other profiles stay in the file.

Run `hoop config` or `hoop config <command> --help` for the command list.

## File

```toml
current = "profile1"

[profile profile1]
token = "<token>"
api_url = "https://profile1.example:8009"
grpc_url = "grpcs://profile1.example:8010"
tls_ca = "<base64-ca>"
skip_tls_verify = false

[profile profile2]
token = "<token>"
api_url = "https://profile2.example:8009"
grpc_url = "grpcs://profile2.example:8010"
tls_ca = "<base64-ca>"
skip_tls_verify = false
```

`[profile.profile1]` is the same section as `[profile profile1]`.

A file with top-level `token`, `api_url`, and `grpc_url` and no `[profile]` sections still loads. `hoop config set-context <name>` turns that file into one profile.

## Switch

```bash
hoop config get-contexts
hoop config use-context profile2
hoop config current-context
```
