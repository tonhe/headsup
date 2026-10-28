# Research: Fathom

Each entry records a decision, why it was made, and what else was considered.

## R1. Transport to headscale

- **Decision**: Use a hand-written `net/http` client against REST `/api/v1`, authenticated with
  `Authorization: Bearer <api key>`. Hide it behind the `api.Backend` interface.
- **Rationale**:
  - headscale `main` (0.30) has removed gRPC, the proto files, and `gen/go`.
  - 0.29.x serves the same `/api/v1/...` paths through grpc-gateway, so one REST client covers
    both 0.29 and 0.30.
  - A REST client works through reverse proxies, is easy to fake in tests, and does not pull in
    the headscale module or gRPC.
- **Alternatives considered**:
  - The gRPC client from `gen/go`: it is gone on main.
  - The oapi-codegen client from `gen/client/v1`: it only exists on main and returns raw
    `*http.Response`. We may generate our own types from `/api/v1/openapi.yaml` later.
  - The Tailscale-compatible `/api/v2` (0.30 and later): a subset, and missing headscale-only
    operations such as register, backfill, and auth approve. We may use it later for OAuth
    clients.
- **Version notes**:
  - 0.30 returns errors as RFC 7807 `application/problem+json`. 0.29 returns
    grpc-gateway `{code, message}`. The client normalises both into `api.Error`.
  - IDs are uint64 encoded as JSON strings.
  - API keys are addressed by prefix.

## R2. Capability detection

- **Decision**: On connect, call `GET /api/v1/health` and `GET /version`, falling back to
  probing. Build a `Capabilities` set with fields such as `PolicyCheck`, `AuthApproval`,
  `OAuthClients`, `PreAuthKeyDelete`, `ProblemJSON`, and `PolicyWritable`.
- **Rationale**: Modules ask `caps.Has(x)` instead of comparing version strings. The server
  page renders the same set as the capability matrix (FR-040). `PolicyWritable` is learned
  lazily, the first time a set-policy call is refused in file mode.

## R3. TUI stack

- **Decision**: Bubble Tea v1.3.x, Bubbles v1, and Lipgloss v1.1, the same as flo and viaduct.
  Add teatest and golden from `charmbracelet/x/exp` for tests.
- **Rationale**: Themes, the pane helper, sparklines, and test harness patterns can be copied
  straight from viaduct. Moving to v2 later is contained, because only `internal/ui` and
  `internal/app` touch Bubble Tea types directly.
- **Alternatives considered**: Bubble Tea v2, which has a better keyboard protocol. It was
  deferred for parity with the sibling projects.

## R4. Theming

- **Decision**: Port viaduct's `internal/theme` package, which has 61 Base16 themes, the
  `Slug`/`Light` fields, and `SparkGradient`. Then:
  - Add user themes: `$XDG_CONFIG_HOME/fathom/themes/*.toml`, in Base16 keys `base00` to
    `base0F` plus `name` and `light`. Standard Base16 YAML schemes are also accepted, so the
    whole tinted-theming catalogue works with no conversion.
  - Add a semantic layer, `theme.Roles`, derived from Base16: `Accent`, `Online`, `Offline`,
    `Expiring`, `Danger`, `Muted`, `Selection`, `TagFG`, `TagBG`, `RouteApproved`, and
    `RoutePending`. Optional `[roles]` overrides can be set in a theme file.
  - Add colour-depth fallback with `termenv` profile detection. Truecolor is downsampled to 256
    or 16 colours, and 16-colour terminals use ANSI.
- **Rationale**: The constitution's principle III, and shared muscle memory across the
  author's tools.

## R5. Modularity

- **Decision**: Use a `module.Module` interface and a global `registry`. Each module lives in
  `internal/modules/<name>/`, registers itself in `init()`, and is enabled by a blank import in
  `internal/modules/all/all.go`. That one import line is the only change outside the module.
- Each module declares `Actions()`. These are the single source for key bindings, the command
  palette, the help screen, read-only gating, and confirmation policy.
- **Rationale**: Meets SC-007 and keeps the palette and help screen in sync by construction.

## R6. Policy handling

- **Decision**:
  - Parse with `github.com/tailscale/hujson`, which handles JSON with comments and trailing
    commas, into our own `policy.Document` model: groups, tagOwners, hosts, acls, grants, ssh,
    autoApprovers, and tests.
  - Validate on the server with `POST /policy/check` when available. Otherwise fall back to a
    local parse check.
  - Show diffs with a line diff (`github.com/sergi/go-diff`) and syntax highlighting with
    `github.com/alecthomas/chroma/v2`, using a style built from the active theme.
  - Keep history as timestamped files in `$XDG_STATE_HOME/fathom/policy/<profile>/`, last 50.
  - Use optimistic concurrency: compare `updatedAt` when the edit started with the current
    value before calling `PUT /policy`.
- **Access preview**:
  - Expand groups and tags into concrete nodes, then evaluate `acls` and `grants` for a chosen
    source.
  - The scope is best-effort and labelled as such. It covers `accept` rules, ports, protocols,
    `autogroup:self`, `autogroup:member`, and `autogroup:tagged`. It does not cover posture
    rules or via-routing.
  - The server's own `tests` stay the source of truth.

## R7. Configuration and secrets

- **Decision**:
  - TOML in `$XDG_CONFIG_HOME/fathom/config.toml`, read with go-toml/v2 (the same library as
    viaduct).
  - `[[profile]]` tables hold `name`, `url`, `api_key` | `api_key_cmd` | `api_key_env` |
    `keyring = true`, `insecure_skip_verify`, `read_only`, and `ca_file`.
  - Keyring support uses `github.com/zalando/go-keyring`.
  - The `api_key_cmd` option supports `pass`, `op read`, and similar tools.
  - Environment variables: `HEADSCALE_URL` and `HEADSCALE_API_KEY`. `HEADSCALE_CLI_ADDRESS`
    and `HEADSCALE_CLI_API_KEY` are also honoured, for parity with the official CLI.
- **Rationale**: This gives zero-config start (principle VI), and secrets stay out of plain
  config for those who want that.

## R8. Clipboard

- **Decision**: Use `github.com/atotto/clipboard` locally. When `SSH_TTY` is set, or the local
  clipboard fails, write OSC52 via `aymanbagabas/go-osc52/v2`.

## R9. QR codes for pre-auth keys

- **Decision**: Use `github.com/skip2/go-qrcode`, rendered as half-block Unicode. The code
  encodes the full `tailscale up --login-server ... --authkey ...` command. It is useful for
  phones running a QR-capable shell, and it looks great in demos.

## R10. Large lists

- **Decision**: Build our own virtualised table component (`ui/table`) that renders only the
  visible rows. Filtering uses a pre-lowered search string per row and `sahilm/fuzzy` for fuzzy
  mode. Sorting is stable, on typed keys.
- **Rationale**: `bubbles/table` renders every row and has no per-cell styling, multi-select,
  or column priority.

## R11. Non-interactive CLI

- **Decision**: Use stdlib `flag` with a small hand-written subcommand router in `internal/cli`,
  matching flo. The CLI calls the same `api.Backend` and the same module `Actions`. The output
  formatter offers `-o table|json|yaml|wide`.
- **Rationale**: This follows the constitution's technical constraints. Each action is written
  once and serves both the TUI and the CLI.

## R12. Auto-refresh and watch

- **Decision**: Poll on a timer, by default every 5s on the dashboard and machines views and
  every 30s elsewhere. Diff each snapshot against the last one to emit `Event`s such as
  `NodeOnline`, `NodeOffline`, `KeyExpiring`, and `RouteAdvertised`. These feed toasts, the
  activity feed, and optional notifications.
- **Rationale**: headscale has no admin event stream.

## R13. Desktop notifications

- **Decision**: Use `github.com/gen2brain/beeep` (no CGO), off by default, with a terminal bell
  option.

---

# Host mode research (added 2026-10-28)

Sources:

- headscale v0.29.6 and main: `config-example.yaml`, `cmd/headscale/cli/*`, `hscontrol/app.go`,
  `hscontrol/debug.go`, `hscontrol/extrarecords.go`, `hscontrol/types/config.go`, and
  `packaging/`.

## R14. Local transport (no API key)

- **Decision**: Pick the local transport by the version that is detected.
  - **0.30 and later**: the unix socket serves HTTP `/api/v1` with no auth (local trust). Reuse
    the `httpapi` client with a unix-socket dialer. This is a new `httpapi.WithUnixSocket(path)`
    option.
  - **0.29.x**: the unix socket speaks gRPC only. Add an `execcli` backend that runs
    `headscale <cmd> -o json` and maps its JSON, which uses 0.29's protobuf JSON field names.
    This avoids depending on gRPC and protobuf for a version that is going away.
  - **Container installs**: the same `execcli` backend, run through
    `docker|podman exec <ctr> headscale ...`.
- **Rationale**: Zero-config on both supported versions, with one small extra backend. The
  `Backend` contract suite (T015) runs against all three backends.
- **Alternatives considered**:
  - A gRPC client for 0.29: adds heavy dependencies for a version that is going away.
  - Requiring an API key locally: breaks the zero-config promise.
- **Socket path**: read `unix_socket` from the detected config. The default is
  `/var/run/headscale/headscale.sock`, with mode `0770 headscale:headscale`.

## R15. Host environment detection

- **Config**: use headscale's own search order. First `--hs-config` or `HEADSCALE_CONFIG`, then
  `/etc/headscale/config.yaml`, then `$HOME/.headscale/config.yaml`, then
  `./config.yaml`. Also try `config.yml`.
- **Service manager**: check in this order.
  - systemd: `systemctl show headscale.service -p ActiveState,SubState,MainPID,ExecMainStartTimestamp,MemoryCurrent`.
  - OpenRC: `rc-service headscale status`.
  - A container named or labelled `headscale` (docker or podman), with the config found through
    its bind mounts.
  - A bare process (`pgrep -x headscale`), which allows signals only.
  - None.
- **Permissions**: probe the socket by connecting to it. Probe files with `access(2)` for read
  and write on each managed file and its directory. Compare the process uid with the file owner.
  Record this as `Perms` in the host environment.
- **Packaging defaults (deb/rpm)**:
  - The service runs as user and group `headscale`.
  - `/etc/headscale/config.yaml` is mode 0644 and is probably owned by root.
  - `/var/lib/headscale` is mode 0750.
  - Writing the config therefore usually needs root. The UI shows that requirement as a fix
    command; it never escalates by itself.

## R16. Editing YAML without losing comments

- **Decision**: Use `gopkg.in/yaml.v3`'s `yaml.Node` tree. Fathom reads typed values from the
  tree and writes edits back into the existing nodes, so comments, key order, and quoting
  survive.
  - New keys are inserted next to related keys.
  - `unix_socket_permission` is always written as a quoted string; headscale refuses to start
    if it is not quoted.
  - Free-form `$EDITOR` edits are loaded back through the same typed schema.
- **Rationale**: Hand-maintained configs are full of comments, and losing them is
  unacceptable.
- **Alternatives considered**: `goccy/go-yaml` also preserves comments, but it is less
  battle-tested for round trips; we may switch to it if yaml.v3 output drifts. Plain
  struct-based yaml marshalling destroys comments.

## R17. Validation and apply semantics per file

| File | Validator | Takes effect |
|---|---|---|
| config.yaml | typed schema + `headscale configtest -c <tmp>` | **restart** (all keys) |
| policy file (`policy.mode: file`) | `hujson` parse + `headscale policy check -f <tmp>` | **reload** (SIGHUP via `systemctl reload` / `kill -HUP`) |
| `dns.extra_records_path` | JSON/YAML schema (name, type ∈ {A, AAAA}, value) | **live** (fsnotify, hash-deduped) |
| DERP map file (`derp.paths`) | schema (regions, nodes, ports) | **restart** (assume; periodic refresh unverified) |

- Validators run on a temporary copy written next to the target, never on the live file.
- `dns.extra_records` (inline) and `extra_records_path` cannot both be set. The DNS editor
  offers to move inline records into a file.
- 0.30 accepts `.yaml`, `.yml`, `.json`, and `.hujson` for map and records files. Fathom keeps
  whatever format the file already uses.

## R18. Atomic writes and backups

1. Read the file and record its sha256 when it is opened.
2. Before writing, re-read it. If the hash has changed, run a three-way merge, or refuse.
3. Copy the current file to `$XDG_STATE_HOME/fathom/backups/<profile>/<ts>/<path>`, with a
   `manifest.json` recording sha256, owner, and mode.
4. Write `<dir>/.<name>.fathom-tmp`, fsync it, `chown` and `chmod` it to match the original,
   rename it over the target, then fsync the directory.
5. Retention keeps the last 50 backups per file, with configurable age.
6. **Secret hygiene (FR-064)**:
   - Every backup and history directory is created with mode 0700 and every file with mode
     0600, using `os.OpenFile(..., 0600)` under a 0077 umask.
   - Before writing, Fathom `lstat`s the backup root and refuses when it is a symlink, has any
     group or other permission bit set, or is owned by another uid. It shows the fix command,
     for example `chmod 700 <dir>`.
   - This applies equally to policy history and the per-write backups in step 3.
   - Manifests never contain file contents or secret values.

When the directory is not writable but the file is (a rare case), fall back to an in-place
truncate-and-write, after the backup.

## R19. Database (read-only enrichment)

- **Decision**: Open SQLite with `modernc.org/sqlite` (pure Go) in read-only mode, using
  `file:...?mode=ro&_pragma=query_only(1)`.
  - Read only from `nodes`: `host_info` (JSON of `tailcfg.Hostinfo`: OS, OSVersion,
    IPNVersion, Hostname, NetInfo.PreferredDERP, NetInfo.DERPLatency) and `endpoints`.
  - Join these to API nodes by ID.
- **Guards**:
  - Check the gormigrate `migrations` table for known IDs. An unknown schema turns the
    enrichment off.
  - Set a per-query timeout.
  - Do not open the database at all if WAL needs directory write access the process lacks.
- **Backups**: use SQLite's online backup API into the backup bundle, which leaves the server
  running.
- **Postgres**: use `pgx` with the configured DSN and `default_transaction_read_only=on`.
- The database is never written. A lint test forbids `INSERT`, `UPDATE`, `DELETE`, `ALTER`,
  and `CREATE` in `internal/host/db`.
- Online state is not stored in the database. It always comes from the API.

## R20. Debug and metrics endpoints (`metrics_listen_addr`, default 127.0.0.1:9090)

Fathom uses these debug endpoints:

| Endpoint | Used for |
|---|---|
| `/debug/registration-cache` | auto-filled pending queue (US3) |
| `/debug/routes` | primary and HA state (US6) |
| `/debug/filter` | compiled packet filter (US5) |
| `/debug/derp` | live DERP map (US15) |
| `/debug/ping` | node ping (US2) |
| `/debug/overview` and `/debug/config` | server page (secrets omitted by headscale) |

- Metrics come from `/metrics`, parsed with `prometheus/common/expfmt`. Series of interest:
  `headscale_nodestore_nodes`, `headscale_mapresponse_sent_total`, `go_memstats_*`, and
  `process_*`. Fathom samples every 5s into ring buffers that feed the dashboard sparklines.
- The response formats are internal to headscale and unversioned. Fixtures are recorded from
  real 0.29.6 and main containers (`scripts/record-debug-fixtures.sh`), and a weekly CI
  shape-drift job re-records them and fails on parser regressions. Each consumer parses
  defensively, and on a parse failure it turns that feature off with a notice.

## R21. Logs

- **Decision**: The log source is an interface with these implementations:
  - `journalctl -u headscale -o json -f -n 500`, run as a subprocess. The go-systemd sdjournal
    package needs CGO, so it is not used.
  - `docker|podman logs -f --tail 500`.
  - Tailing a file.
- Each `MESSAGE` is parsed as JSON when `log.format: json`, and otherwise as zerolog text
  (`time LVL msg key=value`).
- Machine names, node IDs, and IPs found in a line are linked to the machine.

## R22. Service control

- systemd: `systemctl start|stop|restart|reload headscale`.
- OpenRC: `rc-service`.
- Containers: `docker|podman restart`.
- Bare process: `SIGHUP` only.

Restart sequence:

1. Run `configtest`.
2. Take a backup.
3. Restart.
4. Poll health for up to 30s.
5. If headscale is still unhealthy, show the last 50 log lines and offer to restore and
   restart again.

If the call fails with a permission error, show the exact command, for example
`sudo systemctl restart headscale`.

Danger levels follow constitution IV:

| Action | Danger | Notes |
|---|---|---|
| Stop | Critical | Type the hostname to confirm |
| Restart | Confirm | Runs configtest first |
| Start | — | Runs configtest first; refused if it fails |
| Reload | Confirm | Reloads the policy only |

## R23. Upgrade assistant

- Fetch `api.github.com/repos/juanfont/headscale/releases`. This can be turned off, and it
  honours headscale's `disable_check_updates`.
- Compare the latest release with the installed version.
- Enforce the documented upgrade path: one minor version at a time, no downgrades, and 0.30
  only from 0.29.x.
- Render the release notes, and take a backup before the upgrade.
- Fathom does not run package managers. It shows the commands.
