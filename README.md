<div align="center">
  <h1>headsup</h1>
  <p><em>A terminal UI and CLI for administering a headscale server</em></p>

  [![Go](https://img.shields.io/github/go-mod/go-version/tonhe/headsup)](https://go.dev)
  [![Tests](https://github.com/tonhe/headsup/actions/workflows/test.yml/badge.svg)](https://github.com/tonhe/headsup/actions/workflows/test.yml)
  [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
  [![GitHub release](https://img.shields.io/github/v/release/tonhe/headsup)](https://github.com/tonhe/headsup/releases)
  [![macOS](https://img.shields.io/badge/macOS-supported-brightgreen?logo=apple)](https://github.com/tonhe/headsup)
  [![Linux](https://img.shields.io/badge/Linux-supported-brightgreen?logo=linux&logoColor=white)](https://github.com/tonhe/headsup)
  [![Windows](https://img.shields.io/badge/Windows-supported-brightgreen?logo=windows&logoColor=white)](https://github.com/tonhe/headsup)

</div>

Self-hosting your tailnet with headscale means giving up the Tailscale admin
console. The API covers machines, users, keys and the policy. DNS, DERP, OIDC
and every other server setting live in `config.yaml`, which means SSH, an
editor, and hoping you got the YAML right before the restart.

headsup does both from a keyboard. Run it on the headscale server and it
finds headscale on its own, with no API key and no flags, and adds the
server's config, DNS, DERP, service, logs and backups to the usual pages.
Run it anywhere else and it talks to the REST API like any other client.

<img src="docs/demo.gif" alt="headsup on demo data: dashboard, machines, routes, policy, DNS, logs and the theme picker" width="900">

[Install](#install) · [Quick start](#quick-start) · [Features](#features) · [Keys](#keys) · [Themes](#themes) · [Docs](#docs)

---

## How headsup compares

| | headscale web UIs¹ | headsup, remote | headsup, on the server |
|---|:---:|:---:|:---:|
| Machines, users, routes, pre-auth and API keys | Yes | Yes | Yes |
| Policy with `policy.mode: database` | Yes | Yes | Yes |
| Policy with `policy.mode: file` | No | No | Yes |
| DNS, DERP, OIDC and other `config.yaml` settings | No | No | Yes |
| Service start, stop, restart and live logs | No | No | Yes |
| Backups and file restore | No | No | Yes |
| Needs an API key | Yes | Yes | No |
| Runs in a terminal over SSH | No | Yes | Yes |
| Something extra to host and keep patched | Yes | No | No |

¹ What any UI limited to headscale's REST API can reach. headscale has no API
for its config file, service or logs. A web UI that runs on the server with
access to its files could do more, so check the one you're looking at.

[docs/CAPABILITIES.md](docs/CAPABILITIES.md) has the full matrix by headscale
version and mode, and how it maps to the Tailscale console.

---

## Install

### Go

```bash
go install github.com/tonhe/headsup/cmd/headsup@latest
```

Needs the Go version in [`go.mod`](go.mod).

### From source

```bash
git clone https://github.com/tonhe/headsup.git
cd headsup
make install    # builds and copies to ~/bin/
```

### Homebrew (macOS and Linux)

```bash
brew install tonhe/tap/headsup
```

### deb, rpm, apk and binaries

The [releases page](https://github.com/tonhe/headsup/releases) has `.deb`,
`.rpm` and `.apk` packages, and archives for Linux, macOS and Windows on
amd64 and arm64.

---

## Quick start

Try it without a server:

```bash
headsup --demo           # demo tailnet
headsup --demo --host    # demo tailnet plus a simulated server for the Server pages
```

On the headscale server, as root or as a member of the `headscale` group:

```bash
headsup
```

It finds headscale's config, connects over the local socket (0.30) or the
`headscale` CLI (0.29), and asks to restart under `sudo` if this user can't
reach it. `headsup host perms` lists what you can and can't do, with the fix
for each. See [docs/HOST-MODE.md](docs/HOST-MODE.md).

From anywhere else, with an API key:

```bash
# on the server
headscale apikeys create --expiration 90d

# on your machine
export HEADSCALE_API_KEY=...
headsup --url https://headscale.example.com
```

The API key is never accepted as a flag value. It comes from
`HEADSCALE_API_KEY` (or `HEADSUP_API_KEY`), `--api-key-file`, a command, or
the OS keyring. Press `C` in the TUI to save a connection profile with its key
in the keyring, or add one from the shell:

```bash
headsup config profile add homelab --url https://headscale.example.com --api-key-cmd "op read op://infra/headscale/key"
```

The same binary is a scriptable CLI. Changes go through the same checks,
confirmations and activity log as the TUI:

```bash
headsup nodes ls -o json
headsup routes ls --pending
headsup routes approve 5 10.20.0.0/16
headsup keys preauth create --user alice --reusable --expiration 24h
headsup backup create
```

`headsup help` lists every verb. Destructive verbs ask on a terminal and need
`--yes` in scripts. [contracts/cli.md](specs/001-headscale-admin-tui/contracts/cli.md)
has the reference, including exit codes.

Supported headscale versions: 0.29.x and 0.30.

---

## Features

- **Machines.** Filter (`tag:web`, `user:alice`, `is:online`), sort, a
  detail pane, rename, bulk tags, expire, disable expiry, delete with a typed
  confirmation, copy IP or name (OSC 52, so it works over SSH), and ping from
  the server in host mode.
- **Add device.** Register by pasted key or URL, approve or reject web
  logins, and create pre-auth keys with a one-time reveal, the `tailscale up`
  command and a QR code.
- **Routes.** Subnet routes grouped by prefix into HA sets, exit nodes,
  overlap warnings, approve and revoke, and primary/failover markers in host
  mode.
- **Access controls.** Syntax-highlighted policy, inline or `$EDITOR`
  editing, server-side check, diff, apply with a conflict check, history of
  the last 50 revisions with rollback, an explorer of groups, tags, hosts and
  rules, and an access preview. File-mode policies are written on the host
  and reloaded.
- **Server Settings** (host). headscale's `config.yaml` by section, with
  defaults, restart-needed and danger markers, type-aware editors,
  `headscale configtest` before every save, comments preserved, and
  migration of deprecated keys.
- **DNS and DERP** (host). MagicDNS, nameservers, split DNS, search domains
  and records. Records in a records file go live without a restart. The DERP
  page edits the embedded relay, map URLs and map files, and shows a live map
  with per-region machine counts and latency.
- **Service and logs** (host). Start, stop, restart and reload, with a config
  check before every start. A live log viewer with level filter, regex search
  and links to machines.
- **Server.** Capability matrix by mode, a doctor (permissions, socket, TLS
  expiry, deprecated keys, database and disk, clock skew) with a fix for each
  finding, backup bundles with file restore, and an upgrade assistant that
  prints the commands and never runs them.
- **Safe by default.** Destructive changes name their target, and the worst
  ones make you type it. Every host file write is validated, diffed,
  confirmed, backed up, written atomically and one key from rollback.
  headsup never writes the headscale database and never runs `sudo` without
  asking. A read-only mode disables every change.
- **Activity.** A log of this app's changes, and a live feed of tailnet
  events (machines going offline, keys nearing expiry, new routes) with
  optional toasts, bell or desktop notifications.
- **Keyboard first.** Every action has a key, every list filters with `/`,
  and the `:` palette reaches everything. Mouse support is optional.
- **61 themes** plus your own theme files, Unicode, Nerd Font and ASCII
  icons, layouts that fit 80x24, and colours that fall back to 256 or 16
  colours (or none, with `NO_COLOR`).

---

## Keys

Pages are grouped into three sections. Press a section's digit, then the
page's letter. The sidebar and status bar show the letters while you choose.

| Section | Pages |
|---|---|
| `1` Tailnet | `h` Dashboard, `m` Machines, `u` Users, `r` Routes, `p` Add device, `k` Keys, `a` Access controls |
| `2` Server | `S` Server, `s` Server Settings, `d` DNS, `D` DERP, `l` Service and logs |
| `3` App | `v` Activity, `,` Preferences |

`g` plus the same letter (`g m`, `g d`) goes straight to a page from
anywhere, and `enter` (or the digit again) opens a section's first page. The
Server pages other than Server need host mode, and they're left out of the
sidebar in remote mode.

| Key | Action |
|---|---|
| `:` or `ctrl+p` | Command palette: every action, page and theme |
| `?` | Help, generated from the live key map |
| `/` | Filter the current list |
| `]` / `[` | Next / previous page |
| `tab` / `shift+tab` | Move focus |
| `j` `k` `g` `G` `ctrl+d` `ctrl+u` | Move |
| `T` | Theme picker with live preview |
| `,` | Preferences |
| `P` | Switch connection profile |
| `C` | Edit connection |
| `R` or `ctrl+r` | Refresh |
| `ctrl+b` | Toggle the sidebar |
| `q` | Quit (asks first if you have unsaved edits) |

Each page's own keys are in its status bar and in `?`. Every key can be
remapped by action ID under `[keys]` in the config, for example
`"machines.expire" = ["e"]`.

---

## Themes

headsup ships with 61 built-in themes. Tokyo Night is the default. Pick one
with `T`, `--theme <slug>` or `ui.theme` in the config, or set one per
connection profile, so production can look different from the homelab.

<details>
<summary>Show all 61 themes</summary>

| Family | Themes |
|--------|--------|
| **Solarized** | solarized-dark, solarized-light, solarized-dark-hc, solarized-osaka-night, solarized-darcula |
| **Catppuccin** | catppuccin-mocha, catppuccin-macchiato, catppuccin-frappe, catppuccin-latte |
| **Gruvbox** | gruvbox-dark, gruvbox-light, gruvbox-dark-hard, gruvbox-material |
| **Tokyo Night** | tokyo-night (default), tokyo-night-storm, tokyo-night-moon, tokyo-night-day |
| **One/Atom** | one-dark, one-light, one-half-dark, one-half-light |
| **Rosé Pine** | rose-pine, rose-pine-moon, rose-pine-dawn |
| **Kanagawa** | kanagawa-wave, kanagawa-dragon, kanagawa-lotus |
| **Everforest** | everforest-dark, everforest-light |
| **Fox** | nightfox, carbonfox, nordfox, dawnfox |
| **Monokai** | monokai-classic, monokai-pro, monokai-vivid |
| **GitHub** | github-dark, github-dark-dimmed, github-light |
| **Standalone** | dracula, nord, flexoki-dark, flexoki-light, cyberdream, ayu-mirage, ayu-dark, ayu-light, challenger-deep, night-owl, night-owlish-light, doom-one, moonfly, sonokai, oxocarbon, poimandres, vesper, palenight, horizon, zenburn, modus-operandi, tomorrow |

</details>

Your own themes go in `~/.config/headsup/themes/` as TOML or standard Base16
YAML. [docs/THEMES.md](docs/THEMES.md) covers the format and every colour
role. The [`themes/`](themes/) directory has two examples, including a high
contrast theme.

---

## Docs

- [docs/HOST-MODE.md](docs/HOST-MODE.md): permissions, containers, managed
  files and when changes take effect, backups and restore.
- [docs/CAPABILITIES.md](docs/CAPABILITIES.md): what works on which headscale
  version and in which mode, every endpoint and action, and Tailscale console
  parity.
- [docs/THEMES.md](docs/THEMES.md): writing a theme.
- [docs/MODULES.md](docs/MODULES.md): adding a page.
- [CONTRIBUTING.md](CONTRIBUTING.md): workflow, tests and the rules CI
  enforces.
- [CHANGELOG.md](CHANGELOG.md).

## License

MIT. See [LICENSE](LICENSE).
