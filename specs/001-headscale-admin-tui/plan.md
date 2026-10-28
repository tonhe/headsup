# Implementation Plan: Headsup, a Headscale Admin TUI

**Branch**: `001-headscale-admin-tui` | **Date**: 2026-10-28 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/001-headscale-admin-tui/spec.md`

## Summary

Headsup is a keyboard-first, themeable terminal UI and companion CLI for headscale. It is meant
first of all to run on the headscale server. It covers every operation in the headscale admin
API, and in host mode it also covers what headscale only allows through files and the service:
server settings, DNS, DERP, file-mode policy, service control, logs, metrics and debug data,
read-only database detail, doctor, and backup. It is built in Go on Bubble Tea and Lipgloss.

How it is put together:

- Two seams keep the UI independent of where it runs:
  - `api.Backend` has three backends: REST `/api/v1` over TCP or a unix socket, the local
    `headscale -o json` CLI (for 0.29 and containers), and a fake for tests.
  - `host.Host` covers environment detection, managed files with a
    validate→diff→confirm→atomic-write pipeline, service control, logs, metrics and debug data,
    and read-only database access, plus a fake.
  - Without host access, every host feature degrades cleanly to remote mode.
- Each admin area is a self-registering module.
- Each module's action list drives its key bindings, the command palette, the help screen, and
  the CLI.
- Theming reuses viaduct's Base16 engine, extended with user theme files and semantic roles.

## Technical Context

**Language/Version**: Go 1.25+

**Primary Dependencies**:

- UI: bubbletea v1.3, bubbles v1, lipgloss v1.1
- Config: go-toml/v2 (Headsup's own), yaml.v3 Node API (headscale's config, preserving comments)
- Host: modernc.org/sqlite (pure Go, read-only), pgx (read-only), fsnotify,
  prometheus/common/expfmt; journalctl, systemctl, and docker are run as subprocesses
- Policy: tailscale/hujson, chroma/v2, sergi/go-diff
- Search: sahilm/fuzzy
- Platform: go-keyring, atotto/clipboard, go-osc52, skip2/go-qrcode, go-colorful, beeep

**Storage**:

- Config: TOML at `$XDG_CONFIG_HOME/headsup`.
- Policy history, activity log, and host-file backups: `$XDG_STATE_HOME/headsup` (JSONL plus
  files).
- Headsup never writes headscale's database.
- No database.

**Testing**: stdlib `testing` (table-driven), teatest end-to-end tests, golden view snapshots,
an `httptest` contract suite against recorded 0.29 and 0.30 responses, fuzz tests for the
filter parser and policy parser, and an optional integration job against headscale in Docker.

**Target Platform**: Remote mode runs on Linux, macOS, and Windows on amd64 and arm64. Host mode
targets Linux under systemd first, then containers (docker or podman) and OpenRC. It ships as a
static binary.

**Project Type**: CLI/TUI application.

**Performance Goals**:

- First frame in under 1s on a reachable server.
- Filter re-render within one frame (16ms) at 2,000 nodes.
- Idle CPU around 0% between polls.

**Constraints**:

- Usable at 80x24 and in 16-colour terminals.
- No CGO.
- Secrets never logged.

**Scale/Scope**: About 14 modules, about 25 reusable components, and about 90 actions.
Tailnets up to 10k nodes.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | How the design complies | Status |
|---|---|---|
| I. Parity, terminal-native | The capability matrix in `contracts/backend.md` maps every `/api/v1` endpoint to a module action. Every action has a key and a palette entry. | PASS |
| II. Modular | Module interface plus registry. One import line per module. All API calls go through `api.Backend`. | PASS |
| III. Theme everything | `theme` is the only package with colour literals, enforced by a lint test. Supports user TOML/YAML themes and live switching. | PASS |
| IV. Safe by default | `Action.Danger` levels drive mandatory confirm modals. Policy changes go through check, then diff, then confirm. Secrets are masked. There is a read-only mode. Activity log entries are redacted. | PASS |
| V. Test the pixels | Golden tests per view per size (80x24, 120x40) on 3 reference themes, plus an all-themes render smoke test (SC-006). teatest flows. Coverage gate. | PASS |
| VI. Zero-friction | Static binary. Flags and environment variables are enough. goreleaser, Homebrew tap, nfpms, and Scoop. | PASS |
| VII. Community | MIT license. README with VHS demo. CONTRIBUTING, THEMES.md, and MODULES.md. Human-readable errors. | PASS |
| VIII. Host-local first | Zero-flag start on the host through `host.Detect` plus a local transport. Each module declares its `Modes`. Typed model and validator for every managed file. Detection per feature, so each one degrades on its own. Never writes the database (lint test). No self-escalation (shows a fix command). | PASS |

Post-design re-check: PASS. The design adds no complexity beyond what the constitution allows.

## Project Structure

### Documentation (this feature)

```text
specs/001-headscale-admin-tui/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── backend.md        # api.Backend interface + endpoint/capability matrix
│   ├── module.md         # Module/Action/Context contracts
│   ├── theme-file.md     # user theme file format + roles
│   ├── config.md         # config.toml schema
│   └── cli.md            # non-interactive command grammar + output
└── tasks.md
```

### Source Code (repository root)

```text
cmd/headsup/main.go                 # flag parsing → cli router or TUI
internal/
├── version/                       # ldflags-stamped version/build
├── config/                        # TOML load/save, profiles, XDG paths, secret resolution
├── api/                           # Backend interface, domain types, errors, Capabilities
│   ├── httpapi/                   # REST /api/v1 client: TCP (0.29 gateway, 0.30) or unix socket (0.30)
│   ├── execcli/                   # `headscale … -o json` backend (0.29 local, containers)
│   └── fake/                      # in-memory Backend for tests, demos, `--demo`
├── policy/                        # hujson parse, Document model, diff, access preview, history
├── activity/                      # JSONL mutation log (redacted)
├── host/                          # Host interface + Detect(); nothing here imports ui
│   ├── detect/                    # config search, version, service mgr, log source, perms
│   ├── files/                     # managed-file pipeline: hash, backup, validate, diff, atomic write, rollback
│   ├── hsconfig/                  # typed model over yaml.v3 Node for headscale config.yaml (+ field catalog)
│   ├── dnsrec/  derpmap/          # extra-records + DERP map file models/validators
│   ├── service/                   # systemd / openrc / container / process adapters
│   ├── logs/                      # journald / container / file sources + line parser
│   ├── debug/                     # /debug/* + /metrics clients (defensive parsing)
│   ├── db/                        # read-only sqlite/pg enrichment (host_info, endpoints)
│   ├── backup/  doctor/  upgrade/
│   └── fake/                      # in-memory Host for tests + --demo
├── watch/                         # snapshot differ → Events
├── theme/                         # Base16 themes (ported), user themes, Roles, depth fallback
├── icons/                         # unicode / nerd / ascii icon sets
├── keys/                          # global keymap, remapping from config
├── clip/                          # clipboard + OSC52
├── ui/                            # reusable components (no module knowledge)
│   ├── styles/                    # Styles built from theme.Roles
│   ├── pane/                      # labeled rounded border (from viaduct)
│   ├── table/                     # virtualized, sortable, multi-select, column priority
│   ├── filter/                    # query bar + token parser (tag:, user:, is:online…)
│   ├── form/                      # text/toggle/select/duration/tag-list fields
│   ├── confirm/                   # danger-level confirm modal (type-to-confirm for critical)
│   ├── palette/                   # fuzzy command palette
│   ├── toast/                     # transient notifications
│   ├── statusbar/  header/  sidebar/  tabs/  help/
│   ├── codeview/                  # chroma-highlighted scrollable text w/ line numbers
│   ├── diffview/                  # unified/side-by-side diff
│   ├── qr/  sparkline/  badge/  kv/  spinner/  empty/
├── app/                           # root model: router, modals, layout, global keys, refresh loop
├── modules/
│   ├── registry/                  # Register(), All(), ordering, enable/disable
│   ├── all/                       # blank-imports every built-in module
│   ├── dashboard/  machines/  users/  routes/  pending/  keys/
│   ├── policy/  server/  activity/  prefs/      # prefs = Headsup's own settings
│   ├── serverconfig/  dns/  derp/  service/      # host-mode modules (service = status + logs)
└── cli/                           # subcommand router + formatters (table/json/yaml)
testdata/                          # recorded API fixtures (0.29, 0.30), sample policies
themes/                            # (docs) example user theme files
scripts/check-coverage.sh  demo.tape  .goreleaser.yml  Makefile  .github/workflows/
```

**Structure Decision**: This is a single Go module with viaduct-style `internal/` packages.
UI components never import modules. Modules import `ui`, `api`, `theme`, `icons`, and `keys`,
never one another. Cross-module jumps, such as going from a user to their machines, go through
`app.Navigate(moduleID, args)`.

## Architecture Notes

### Layout

```text
╭ headsup ─ host:hs01 ▾ ─ headscale v0.29.6 ● ─────────────── 42 online / 51 ─╮
│ ▸ Dashboard   │ ╭ Machines ─ 51 ─ filter: tag:server is:online ────────────╮ │
│   Machines    │ │ ● nas-01       alice   100.64.0.3   tag:server  2m ago  │ │
│   Users       │ │ ○ laptop       bob     100.64.0.9   -          3d ago  │ │
│   Routes  (2) │ │ ...                                                     │ │
│   Pending (1) │ ╰─────────────────────────────────────────────────────────╯ │
│   Keys        │ ╭ nas-01 ─ linux 6.8 · 1.84.2 · DERP fra 18ms ────────────╮ │
│   Policy      │ │ IPs  100.64.0.3  fd7a:115c:a1e0::3                      │ │
│   DNS         │ │ Routes ✓ 10.0.0.0/24 (primary)  ◌ 0.0.0.0/0 (pending)   │ │
│   DERP        │ ╰─────────────────────────────────────────────────────────╯ │
│   Settings    │                                                             │
│   Service  ●  │                                                             │
│   Server      │                                                             │
│   Activity    │                                                             │
╰ / filter  enter open  r rename  t tags  e expire  a routes  : palette  ? help╯
```

- **Sidebar**: shown at width 100 and above, collapsible with `ctrl+b`. Below 100 it becomes a
  tab bar. Number keys `1`–`9` jump to modules. Badge counts come from module `Badge()`.
- **Detail pane**: on wide screens it splits right; on narrow screens it splits below. Under 30
  rows it becomes a full-screen push view.
- **Modals**: confirm, form, palette, theme picker, profile switcher, and help. They stack and
  are drawn centred over a dimmed background.

### Action pipeline

When a key or palette entry fires an `Action`:

1. Check `Enabled(ctx)`. A disabled action shows its reason in a toast.
2. If `ReadOnly` is set and `Mutating` is true, block the action with a reason.
3. Collect any parameters through a form.
4. Confirm according to `Danger`: `None` needs nothing, `Confirm` needs y/N, and `Critical`
   needs the user to type the target name.
5. Run the backend call as a `tea.Cmd` behind a spinner.
6. On the result, show a toast, append to the activity log, trigger a targeted refresh, and
   apply an optimistic update.

### Data flow

- `app` owns a `Store`, a cached snapshot of nodes, users, keys, and the policy, refreshed by
  polling. Modules read from the store and request refreshes.
- The `watch` package diffs successive snapshots to produce events.
- A mutation invalidates the slices of the store it affects.

### Modes

- At startup, `host.Detect()` runs before connecting. When it finds a usable local transport
  it builds a host profile automatically, named `host:<hostname>`.
- Each module declares `Modes() module.Modes`, one of `API`, `Host`, `APIHost`, or `Local`.
  - In remote mode, host-only modules still appear in the sidebar but are dimmed. They open to
    an explainer that says what host mode would enable.
  - Host modules that lack a permission stay usable read-only and show a fix-command banner.

### Managed-file pipeline (shared by serverconfig, dns, derp, and file-mode policy)

```text
open:   read → sha256 → typed model (yaml.Node / hujson / json)
edit:   form or $EDITOR → re-parse into the model
apply:  re-read & hash-check → (changed? 3-way diff : continue)
        → write temp copy → schema validate → headscale validator (configtest / policy check)
        → diff view → confirm (Danger by blast radius) → backup → atomic write (keep owner/mode)
        → effect: live | reload (SIGHUP) | restart (offer) → post-health check → toast
        → activity log → "u" to roll back for 5 minutes, then available from history
```

### Error UX

`api.Error` carries `Kind`: `Unreachable`, `Unauthorized`, `NotFound`, `Conflict`,
`Unsupported`, `Invalid`, or `Server`. Each kind maps to a human message and a suggested
action. `Unauthorized` opens the connection editor; `Unsupported` explains which server
version is needed.

## Phasing (MVP first)

| Phase | Delivers | Stories |
|---|---|---|
| 0 Foundation | config, api+fake+httpapi, theme port, ui primitives, app shell, registry | none (infrastructure) |
| 1 MVP | dashboard, machines (full), pending + register + pre-auth create | US1, US2, US3 |
| 2 Admin | users, routes, keys, policy (view/edit/check/diff/apply) | US4–US7 |
| 2H Host core | host detect, local transports (socket, execcli), managed-file pipeline, service and logs, DB and debug enrichment | US14, host parts of US1–US3, US5, US6 |
| 3 Delight | theme picker/user themes/icons/density, palette, remapping | US8, US9 |
| 3H Host settings | server settings editor, DNS, file-mode policy | US10, US13 |
| 4 Ops | server page/doctor/backup/upgrade, DERP, CLI, activity, watch/notify, access preview | US11, US12, US15, US16 |
| 5 Launch | README + VHS demo, docs, goreleaser/brew/scoop/nfpm, CI matrix | — |

## Complexity Tracking

No violations.
