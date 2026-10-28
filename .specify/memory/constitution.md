# Fathom Constitution

Fathom is a terminal UI for administering a headscale control server. It is meant first of all
to run on the headscale host itself. It exists so that people who self-host their tailnet can
do everything they would do in the Tailscale admin console, and the server-side work the
console never needed (config, DNS, DERP, the service, logs), from a keyboard, over SSH, without
running a web app.

## Core Principles

### I. Beyond Web-UI Parity, Terminal-Native

- Every capability the headscale API exposes MUST be reachable from the TUI.
- When running on the host, the TUI SHOULD also cover what headscale only allows through its
  files and service, such as DNS, DERP, OIDC, and other server settings, so that console parity
  is reached wherever the host makes it possible.
- When something is impossible in the current mode, remote or host, the TUI MUST say so and
  name the mode that would enable it, rather than hide the gap.
- Interactions MUST be keyboard-first: every action has a key, every list is filterable, and
  nothing requires a mouse. Mouse support is a nice extra, never the only way to do something.
- Rationale: parity is the reason to use the tool; terminal-native is the reason to prefer it.

### II. Modular by Construction

- Each functional area (machines, users, keys, routes, policy, and so on) MUST be a
  self-contained module that registers itself with the app: its view, key bindings, commands,
  and help text. Adding a module MUST NOT require editing other modules.
- The UI MUST talk to headscale only through a backend interface, so tests can use a fake and
  alternative transports (REST, local unix socket, the local `headscale` CLI) can be swapped in.
- Host access (files, service manager, logs, database, metrics) MUST sit behind its own `host`
  interface, with a fake for tests. Modules MUST NOT touch the filesystem or spawn processes
  directly.
- Modules MAY be disabled by configuration.
- Rationale: contributors should be able to add a screen in one directory.

### III. Theme Everything

- No colour literal may appear outside the theme package. All styles MUST derive from the
  Base16 palette semantics shared with the author's other TUIs (flo, viaduct).
- The app MUST ship many built-in themes, support user theme files, and switch themes at
  runtime with live preview.
- Layouts MUST degrade gracefully down to 80x24 and to terminals without truecolor.
- Rationale: a tool people live in should look the way they want.

### IV. Safe by Default (NON-NEGOTIABLE)

- Destructive or access-changing operations (delete, expire, revoke, policy write, route
  approval) MUST require explicit confirmation that names the exact target.
- Policy edits MUST be validated and shown as a diff before they are applied.
- API keys and pre-auth keys MUST be masked in the UI unless the user deliberately reveals
  them. Secrets MUST never be written to logs.
- A read-only mode MUST exist that disables every mutating action.
- Host file edits MUST follow this exact sequence:
  1. Back up the file with a timestamp.
  2. Edit in a way that preserves comments and formatting.
  3. Validate the result: a schema check, plus headscale's own check where one exists.
  4. Show a diff.
  5. Get explicit confirmation.
  6. Write atomically (temp file, fsync, rename), keeping the original owner and mode.
  7. Offer a one-key rollback.
- Fathom MUST NEVER write to the headscale database. Database access is read-only, opened in a
  way that cannot block or corrupt the running server.
- Fathom MUST NEVER escalate privileges on its own. When an action needs more rights than the
  process has, the TUI explains why and shows the exact command to run (for example with
  `sudo`). It does not run `sudo` itself.
- Service restarts MUST say what they will interrupt (connected clients reconnect) and MUST be
  preceded by a successful config validation.
- Rationale: this tool controls who can reach a private network, and on the host it can also
  take that network down.

### V. Test the Pixels

- Logic is covered by table-driven tests using the stdlib `testing` package. Views are covered
  by golden-file snapshots, and flows are covered by end-to-end TUI tests against a fake
  backend.
- Coverage floors per package MAY go up, never down.
- Every bug fix ships with a test that fails without it.

### VI. Zero-Friction Install

- The app ships as a single static binary with no runtime dependencies, for Linux, macOS, and
  Windows on amd64 and arm64.
- It MUST work with no config file when given a server address and an API key through flags
  or environment variables.
- On the headscale host, it MUST work with zero flags. It finds headscale's config and connects
  through the local socket or CLI with no API key, wherever the current user has permission.
- Startup to first rendered screen SHOULD take under one second on a reachable server.

### VII. Community First

- MIT licensed. The README, contributing guide, and theme-authoring guide are first-class
  deliverables, not afterthoughts.
- Errors MUST be human-readable and say what to try next.
- Supported headscale versions MUST be documented, and incompatibility MUST be detected and
  reported at connect time.

### VIII. Host-Local First

- Host mode is the primary experience, and remote API mode is fully supported as a second mode.
  Every feature declares which modes it needs: `api`, `host`, or `api+host`.
- Every host file Fathom can edit has a typed model and a validator. Free-form editing in
  `$EDITOR` is always available as an escape hatch, and it goes through the same validate,
  diff, and confirm pipeline.
- Host features MUST detect their environment rather than assume it. That covers config path,
  service manager (systemd, OpenRC, Docker, or none), log source, and file permissions. A
  missing piece degrades that feature only, never the whole app.
- Rationale: most headscale servers are single boxes administered over SSH. That is where a
  TUI is better than a web UI.

## Technical Constraints

- Go (latest stable), Bubble Tea, Bubbles, Lipgloss: the same stack as flo and viaduct, so code
  and themes can be shared.
- Configuration is TOML at XDG paths (`$XDG_CONFIG_HOME/fathom/`), with a Windows APPDATA
  fallback.
- CLI flags use the stdlib `flag` package, matching sibling projects.
- No CGO. Releases are built with goreleaser. SQLite reads use a pure-Go driver.
- Host mode targets Linux, as amd64 and arm64 on systemd distributions, plus containers. Host
  features on macOS, Windows, and BSD are best-effort. Remote mode works everywhere.

## Development Workflow

- Spec-driven: specify, then plan, then tasks, then implement, using spec-kit artifacts under
  `specs/`.
- `make test-all` (build, race, coverage gate) MUST pass before merge.
- CI runs on Linux, macOS, and Windows.
- User-visible changes update CHANGELOG.md.

## Governance

This constitution supersedes conflicting practice. Amendments are made by editing this file
with a version bump: MAJOR when a principle is removed or redefined, MINOR when one is added or
expanded, PATCH for wording. Every plan's Constitution Check gates on these principles, and any
violation MUST be justified in that plan's Complexity Tracking table.

**Version**: 1.1.0 | **Ratified**: 2026-10-28 | **Last Amended**: 2026-10-28
