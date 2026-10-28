# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added

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
