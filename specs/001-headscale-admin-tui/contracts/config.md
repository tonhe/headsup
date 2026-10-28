# Contract: `config.toml`

Path: `$XDG_CONFIG_HOME/headsup/config.toml`. Use `headsup config path` to print it.

Precedence, highest first: flags, then environment variables, then the profile, then
`[defaults]`, then built-in defaults.

```toml
default_profile = "homelab"

[ui]
theme          = "tokyo-night"   # any built-in slug or user theme slug
icons          = "unicode"       # unicode | nerd | ascii
density        = "comfortable"   # compact | comfortable
sidebar        = "auto"          # auto | always | never
mouse          = true
time_format    = "relative"      # relative | rfc3339 | local
expiry_warn    = "168h"
refresh        = "5s"            # active view poll interval
refresh_idle   = "30s"

[ui.columns]
machines = ["status", "name", "user", "ips", "tags", "last_seen", "expiry", "routes"]

[modules]
disabled = []                    # e.g. ["activity"]

[keys]                           # action id → keys
"machines.expire" = ["e"]
"app.palette"     = [":", "ctrl+p"]

[notify]
desktop = false
bell    = false
events  = ["node_offline", "key_expiring", "route_advertised"]

[host]
enabled         = "auto"          # auto | always | never
headscale_config = ""             # override config discovery
container       = ""              # e.g. "headscale" to force container mode
backup_dir      = ""              # default $XDG_STATE_HOME/headsup/backups; must be 0700 and owned by you (backups hold private keys)
backup_keep     = 50
db_enrich       = true            # read-only host_info enrichment
debug_addr      = ""              # override metrics_listen_addr
check_updates   = true

[host.doctor]                     # thresholds (spec US16)
cert_warn  = "504h"   # 21d
cert_error = "168h"   # 7d
wal_warn_mb = 64
disk_warn_pct = 10
skew_warn = "2s"

[[profile]]
name    = "homelab"
url     = "https://hs.example.com"
keyring = true                   # key stored in OS keychain under headsup/<name>

[[profile]]
name        = "work"
url         = "https://headscale.corp.internal"
api_key_cmd = "op read op://infra/headscale/credential"
ca_file     = "~/.config/headsup/corp-ca.pem"
read_only   = true
theme       = "gruvbox-dark"     # per-profile theme: visual cue for prod
```

## Flags

```text
--profile, -p NAME     --url URL     --api-key-file PATH
--theme SLUG           --icons SET   --read-only
--insecure             --demo        --config PATH   --version
--host | --remote      --hs-config PATH                  # force/skip host mode
```

The API key is never accepted as a plain flag value, because it would show up in shell history
and `ps`. It is read from the environment, from a file, from a command, or from the keyring.

## Environment variables

- `HEADSUP_URL` / `HEADSCALE_URL` / `HEADSCALE_CLI_ADDRESS`
- `HEADSUP_API_KEY` / `HEADSCALE_API_KEY` / `HEADSCALE_CLI_API_KEY`
- `HEADSUP_PROFILE`, `HEADSUP_THEME`, `NO_COLOR` (honoured)
- `HEADSCALE_CONFIG` (honoured for discovery, the same as headscale itself)

When no profile, URL, or key is given, the precedence is: a usable host mode, then
`default_profile`, then the connection editor.
