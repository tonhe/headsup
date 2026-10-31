# Changelog

All notable changes to this project are documented here.

## [0.1.0] - Unreleased

First release. headsup is a terminal UI and CLI for administering a
headscale server. It works remotely over the REST API (headscale 0.29.x and
0.30) and, run on the headscale server itself, in host mode with no API key.

### Getting around

- Pages are grouped into three sections. Press a section's digit (`1`
  Tailnet, `2` Server, `3` App) and the section lights up with each page's
  key; press the key to open the page (`1 m` Machines, `2 d` DNS, `3 v`
  Activity). `enter` or the digit again opens the section's first page, and
  `esc` cancels.
- Every page also has a `g` chord (`g m`, `g d`, ...), shown dimmed in the
  sidebar, and `,` opens Preferences.
- Command palette (`:` or `ctrl+p`) for every action, "go to" each page and
  "theme: <name>", recent first. Help (`?`) is generated from the actions and
  the live keymap, so remapped keys show.
- Optional mouse support (`ui.mouse`): click the sidebar and section
  headers, scroll lists.

### Tailnet pages

- Dashboard: tailnet counts, sparklines and a "needs attention" list.
- Machines: filter (`tag:`, `user:`, `is:online` ...), sort, detail pane,
  rename, tags (bulk), route approval, expire, disable expiry, delete (typed
  confirm), copy IP or name (OSC52 over SSH). In host mode also OS, client
  version, DERP region and latency, endpoints and ping.
- Users: machine and key counts, create, rename, delete (blocked while the
  user owns machines, with a jump to them), and jumps to the user's machines
  and keys.
- Routes: subnet routes grouped by prefix into HA sets, exit nodes in their
  own section, overlap highlighting, approve, revoke and approve-all, a
  pending badge, and primary/failover markers in host mode.
- Add device: register by pasted URL or key, approve or reject web logins,
  and create pre-auth keys with a one-time reveal, the `tailscale up` command
  and a QR code.
- Keys: pre-auth and API keys with create, expire and delete, an expiry
  colour ramp, and a lockout warning before expiring the API key this app
  uses.
- Access controls: the policy with syntax highlighting, edited inline or in
  `$EDITOR`, then check, diff and apply with a conflict check. History of
  the last 50 revisions with rollback, an explorer of groups, tags, hosts and
  rules with "where used", and an access preview (`p`) that shows what a
  chosen source can reach (best-effort). In file mode on the host, apply
  writes the policy file, reloads headscale and confirms the new policy is
  active, offering rollback if not. In host mode, also the compiled filter.

### Server pages

- Server: version and health, and a capability matrix by mode that explains
  config-file-only features. Backfill IPs. In host mode, a doctor
  (permissions, socket, `server_url` reachability, TLS expiry, deprecated
  keys, database and WAL size, disk space, clock skew) where every finding
  has a fix; backup bundles of the config, policy, records, DERP files, keys
  and an online SQLite copy, with file-only restore; and an upgrade assistant
  that enforces one minor version at a time and prints the commands to run
  without running them.
- Server Settings (host mode): edit headscale's `config.yaml` by section,
  with value, default, restart-needed and danger markers and type-aware
  editors. Every change goes through validation (schema plus `headscale
  configtest`), a diff, confirmation and an atomic write with a backup, then
  an offer to restart, with restore-and-restart if headscale comes back
  unhealthy. Comments are kept. Also `$EDITOR` with a three-way merge when
  the file changed on disk, and a one-key migration of 0.29's flat TLS keys
  to 0.30's `tls.*`.
- DNS (host mode): MagicDNS, base domain, override local DNS, nameservers,
  split DNS, search domains and a records table. Records in a records file go
  live without a restart; everything else is marked "needs restart". One
  confirmed action moves inline records into a file. Record files keep their
  JSON, HuJSON or YAML format and comments.
- DERP (host mode): embedded server settings, map URLs and files, a region
  and node editor for map files, and a live map with per-region machine
  counts and latency.
- Service and logs (host mode): state, since, PID, memory and version, with
  start, stop (type the hostname), restart and reload. Start and restart run
  `headscale configtest` first and refuse on failure. A live log viewer with
  a 10k-line buffer, level filter, regex search, pause, jump to error, field
  expansion and links to machines. The dashboard, sidebar and status bar show
  the service state.

### App pages

- Activity: this app's changes (targets, results, backup IDs and diff
  stats) and a live feed of tailnet events: machines coming online or going
  offline, keys nearing expiry, new advertised routes. Filter with `/`,
  export to JSON with `x`. Events can also raise toasts, the terminal bell or
  desktop notifications (`[notify]`).
- Preferences: theme, icons, density, sidebar, mouse, time format, refresh
  intervals and columns.

### Host mode

- Run `headsup` with no flags on the headscale server. It finds headscale's
  config, connects over the local socket (0.30) or the headscale CLI (0.29) without
  an API key, and supports headscale in a Docker or Podman container
  (`[host].container`).
- When this user can't reach headscale, it explains exactly what is missing
  and offers to restart under sudo; it never escalates without asking.
  `headsup host info` lists permissions with fix commands.
- Every file change keeps a backup (`[host] backup_dir`, `backup_keep`), and
  quitting with unsaved edits asks first.

### CLI

- `headsup VERB ...` for nodes, routes, users, keys, auth, policy, server,
  host, svc, conf, dns, derp, backup and upgrade, with table, wide, json and
  yaml output. Changes run the same actions as the TUI, with the same checks,
  confirmation and activity log. Destructive verbs prompt on a terminal and
  need `--yes` otherwise (exit 7). Exit 8 means no host mode and 9 a missing
  permission, with the fix on stderr.

### Look and feel

- 61 built-in themes plus user theme files (TOML or Base16 YAML), a theme
  picker (`T`) with live preview and per-profile override, and a High
  Contrast theme where every text colour clears 7:1.
- Unicode, Nerd Font and ASCII icon sets. Every screen stays readable in 16
  colours and with `NO_COLOR`: selection, focus and warnings show in the
  text, not only in colour.
- Connection editor (API keys stored in the OS keyring), profile switcher,
  read-only mode, and `--demo` with realistic data (`--demo --host` for host
  mode).

### Quality

- Tested on Linux, macOS and Windows. Integration tests run the quickstart
  scenarios against real headscale 0.29.6 and headscale main in Docker, and
  check the `/debug` and `/metrics` shapes headsup parses.
- A filter keystroke on 2,000 machines renders a frame in about 3 ms.
