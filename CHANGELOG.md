# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added

- Scriptable CLI: `headsup VERB ...` for nodes, routes, users, keys, auth,
  policy, server, host, svc, conf, dns, derp, backup and upgrade, with
  table/wide/json/yaml output. Changes run the same actions as the TUI
  (same checks, confirmation and activity log); destructive verbs prompt on
  a terminal and need `--yes` otherwise (exit 7). Exit 8 means no host mode
  and 9 a missing permission, with the fix on stderr.
- Activity (`9`): this app's changes (with targets, results, backup IDs and
- Activity (`3 v`, `g v`): this app's changes (with targets, results, backup IDs and
  diff stats) and a live feed of tailnet events: machines coming online or
  going offline, keys nearing expiry, new advertised routes. Filter with `/`,
  export to JSON with `x`. Events can also raise toasts, the terminal bell or
  desktop notifications (`[notify]`).
- Access preview in Access Controls (`p`): pick a source and see what it can
  reach under the current (or unsaved) policy. Best-effort.
- Quitting with unsaved edits in Access Controls, Server Settings, DNS or
  DERP now asks first. The statusbar shows the service state in host mode.
- Server page (`2 S`, `g S`): version and health, a capability matrix by mode with
  explanations for config-file-only features, and backfill IPs. In host
  mode: a doctor (permissions, socket, server_url reachability, TLS expiry,
  deprecated keys, database and WAL size, disk space, clock skew), each
  finding with a fix; backup bundles of the config, policy, records, DERP
  files, keys and an online SQLite copy, with file-only restore; and an
  upgrade assistant that enforces one minor version at a time and prints the
  commands to run without running them.
- DNS (host mode, `g d`): MagicDNS, base domain, override local DNS,
  nameservers, split DNS, search domains and a records table. Records in a
  records file go live without a restart; everything else is marked
  "needs restart". One confirmed action moves inline records into a file.
  Record files keep their JSON, HuJSON or YAML format and comments.
- DERP (host mode, `g D`): embedded server settings, map URLs and files, a
  region and node editor for map files, and a live map with per-region
  machine counts and latency.
- Access Controls (`1 a`, `g a`): view the policy with syntax highlighting,
  edit inline or in `$EDITOR`, check, diff and apply with a conflict check;
  history of the last 50 revisions with rollback; an explorer of groups,
  tags, hosts and rules with "where used"; and the compiled filter in host
  mode. In file mode on the host, apply writes the policy file through the
  managed-file pipeline, reloads headscale and confirms the new policy is
  active, offering rollback if not.
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

### Changed

- Page navigation is now two keys. The sidebar groups pages under section
  headers: `1` Tailnet, `2` Server, `3` App. Press a section's digit and the
  section lights up, each of its pages shows its key, and the status bar
  lists them; then press the page's key (`1 m` Machines, `2 d` DNS, `3 v`
  Activity). `enter` or the digit again opens the section's first page;
  `esc` or any other key cancels. The flat `1`–`9` page keys are gone, so
  every page is reachable, not just the first nine. `g` chords (shown dimmed
  in the sidebar), `,` for Preferences and the palette work as before.
  Host-only pages are no longer listed in remote mode (their `g` chords still
  explain host mode). Clicking a section header opens its first page.

### Fixed

- DNS: removing a split DNS domain is now discoverable (`D remove domain`
  on a split row, or clear its servers in the edit form), works when the
  split map is written inline, and leaves `split: {}` when the last domain
  goes.
- Policies written through the local host no longer lose their comments
  (the schema check was rewriting its input).
- Flow tests no longer flake on loaded CI runners. One shared headless driver
  (`uitest.Driver`) runs the app in a `testing/synctest` bubble with a fake
  clock: real work always finishes, timers are parked instead of guessed by
  wall-clock duration, and the suites run several times faster.
- Host mode on headscale 0.27+: `headscale version` no longer gets `-c`, which
  that command rejects, so the CLI transport connects again.
- A socket hidden behind an unreadable directory is now reported as missing
  access (with the group fix), not as "does not exist (is headscale running?)".
