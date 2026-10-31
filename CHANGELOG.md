# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added

- DNS (host mode, `g d`): MagicDNS, base domain, override local DNS,
  nameservers, split DNS, search domains and a records table. Records in a
  records file go live without a restart; everything else is marked
  "needs restart". One confirmed action moves inline records into a file.
  Record files keep their JSON, HuJSON or YAML format and comments.
- DERP (host mode, `g D`): embedded server settings, map URLs and files, a
  region and node editor for map files, and a live map with per-region
  machine counts and latency.
- Users: list with machine and key counts, create, rename, delete (blocked
  while the user owns machines, with a jump to them), and jumps to the user's
  machines and keys.
- Routes: subnet routes grouped by prefix into HA sets, a separate exit-node
  section, overlap highlighting, approve/revoke/approve-all, a pending badge,
  and primary/failover markers in host mode.
- Keys: pre-auth and API key tabs with create (one-time secret reveal, QR),
  expire and delete, an expiry colour ramp, and a lockout warning before
  expiring the API key this app uses.
- Service and logs (host mode, `g l`): status panel (state, since, PID,
  memory, version) with start, stop (type the hostname), restart and reload.
  Start and restart run `headscale configtest` first and refuse on failure.
  Live log viewer with a 10k-line buffer, level filter, regex search, pause,
  jump to error, field expansion and links to machines. The dashboard and
  sidebar show the service state.
- Command palette (`:` or `ctrl+p`): every action, "go to" each page and
  "theme: <name>", recent first. Help (`?`) is generated from the actions and
  the live keymap, so remapped keys show.
- Theme picker (`T`) with live preview, search, light/dark filter, user-theme
  reload and per-profile override. Preferences page (`,`) for theme, icons,
  density, sidebar, mouse, time format, refresh intervals and columns.
- Optional mouse support (`ui.mouse`): click the sidebar, scroll lists.
- The High Contrast theme now clears 7:1 contrast for all text colours.
- Server Settings (host mode, `g s`): edit headscale's config.yaml by section,
  with value, default, restart-needed and danger markers per field and
  type-aware editors. Changes go through validation (schema plus
  `headscale configtest`), a diff, confirmation, an atomic write with a
  backup, and an offer to restart, with restore-and-restart if headscale
  comes back unhealthy. Comments are preserved. Also `$EDITOR` with a
  three-way merge when the file changed on disk, and a one-key migration of
  0.29's flat TLS keys to 0.30's `tls.*`.
- Host mode asks to restart under sudo when this user can't reach headscale,
  instead of only printing the fix.
- Host mode: run with no flags on the headscale server. It finds headscale's
  config, connects over the local socket (0.30) or CLI (0.29) without an API
  key, and adds OS, client version, DERP and endpoint detail, route primaries,
  live metrics and the server's pending-login count. `headsup host info` explains
  permissions with exact fix commands.
- Remote mode over REST `/api/v1` for headscale 0.29.x and 0.30.
- Dashboard with tailnet counts, sparklines and a "needs attention" list.
- Machines: filter (`tag:`, `user:`, `is:online` ...), sort, detail pane, rename,
  tags (bulk), route approval, expire, disable expiry, delete (typed confirm),
  copy IP/name (OSC52 over SSH), ping (host mode).
- Add device: register by pasted URL or key, approve/reject web logins, create
  pre-auth keys with a one-time reveal, `tailscale up` command and QR code.
- Connection editor (keys stored in the OS keyring) and profile switcher.
- 61 built-in themes plus user theme files (TOML or Base16 YAML); Unicode,
  Nerd Font and ASCII icon sets; read-only mode; activity log.
- `--demo` mode with realistic data (`--demo --host` for host mode).

### Fixed

- Host mode on headscale 0.27+: `headscale version` no longer gets `-c`, which
  that command rejects, so the CLI transport connects again.
- A socket hidden behind an unreadable directory is now reported as missing
  access (with the group fix), not as "does not exist (is headscale running?)".
