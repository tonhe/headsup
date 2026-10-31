# Tasks: Headsup, a Headscale Admin TUI

**Input**: Design documents from `/specs/001-headscale-admin-tui/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Required. Constitution principle V says logic gets table-driven tests, views get
golden snapshots, and flows get teatest runs against `fake.Backend`. Within each story, write
the test tasks first and make sure they fail before implementing.

**Format**: `- [ ] [ID] [P?] [Story?] Description with file path`. `[P]` means the task can run
in parallel with others, because it touches different files and has no unfinished
dependencies.

---

## Phase 1: Setup

- [X] T001 Initialise the Go module `github.com/tonhe/headsup` with Go 1.25 in go.mod, and add deps from plan.md
- [X] T002 [P] Create the directory skeleton from plan.md under cmd/ and internal/ with doc.go package comments
- [X] T003 [P] Create a Makefile with build, install, test, test-race, test-all, test-tui, golden-update, coverage-gate, test-integration, release-dry-run, and demo, using ldflags into internal/version (copy viaduct's pattern)
- [X] T004 [P] Add internal/version/version.go with Version, Build, and Commit variables
- [X] T005 [P] Add .goreleaser.yml (v2): CGO off, darwin/linux/windows on amd64/arm64, archives, checksums, nfpms (deb/rpm/apk), a Homebrew tap, and a Scoop bucket
- [X] T006 [P] Add .github/workflows/test.yml, a matrix of ubuntu, macos, and windows running build, race, and the coverage gate
- [X] T007 [P] Add .github/workflows/release.yml (tag to goreleaser) and .github/workflows/integration.yml (docker headscale 0.29.6 plus a main-branch image)
- [X] T008 [P] Add scripts/check-coverage.sh with per-package floors in scripts/coverage-floors.txt
- [X] T009 [P] Add LICENSE (MIT, 2026 Tony Mattke), CLAUDE.md working rules, .gitignore, and a CHANGELOG.md stub
- [X] T010 [P] Add a lint test in internal/theme/nocolor_test.go that fails if `lipgloss.Color(` or a hex literal appears outside internal/theme

---

## Phase 2: Foundational (blocks every story)

### Domain and backend

- [X] T011 Define domain types Node, User, PreAuthKey, APIKey, Policy, Capabilities, and Secret in internal/api/types.go, following data-model.md. Include the derived fields IsExitNode, PendingRoutes, Expired, ExpiresSoon, and Status.
- [X] T012 [P] Write table tests for the Node derived fields (exit node requires both `0.0.0.0/0` and `::/0`, and Idle means last seen under 24h ago) in internal/api/types_test.go
- [X] T013 Define the `api.Error` kinds (Unreachable, Unauthorized, NotFound, Conflict, Unsupported, Invalid, Server) with Hint, Line, and Col in internal/api/errors.go
- [X] T014 Define the Backend interface exactly as in contracts/backend.md in internal/api/backend.go
- [X] T015 [P] Write a shared Backend contract suite, `RunContract(t, newBackend)`, in internal/api/apitest/contract.go
- [X] T016 Implement the in-memory fake.Backend with seed loading, `WithLatency`, and `FailNext` in internal/api/fake/fake.go, and make it pass the contract suite
- [X] T017 [P] Create a seed dataset of 51 nodes, 4 users, pending routes, an exit node, expiring keys, and a sample policy in testdata/seed/*.json
- [X] T018 [P] Record HTTP fixtures for 0.29.6 (grpc-gateway errors) and 0.30 (problem+json) in testdata/http/{v029,v030}/
- [X] T019 Implement the httpapi client: bearer auth, timeouts, TLS/CA/insecure options, uint64-as-string JSON, and normalisation of both error formats, in internal/api/httpapi/client.go
- [X] T020 Implement every Backend method in internal/api/httpapi/{nodes,users,keys,policy,auth}.go, following the endpoint matrix
- [X] T021 Implement capability detection in Connect (health, version, probing) in internal/api/httpapi/caps.go
- [X] T022 [P] Run the httpapi contract tests against httptest servers replaying the v029 and v030 fixtures in internal/api/httpapi/client_test.go
- [X] T023 [P] Add a unix-socket dialer option (`httpapi.WithUnixSocket`) for 0.30 local trust, and run the contract suite over a socket, in internal/api/httpapi/socket.go
- [X] T024 [P] Build the execcli backend: run `headscale <cmd> -o json` (directly or through `docker|podman exec`), and map both the 0.29 protobuf JSON and the 0.30 JSON shapes, in internal/api/execcli/execcli.go
- [X] T025 [P] Run the execcli contract tests against recorded CLI output in testdata/cli/{v029,v030}/ in internal/api/execcli/execcli_test.go

### Config and platform

- [X] T026 Implement XDG paths for config, state, and themes (with an APPDATA fallback) in internal/config/paths.go
- [X] T027 Implement the config schema, defaults, load and save, and precedence (flags > env > profile > defaults) per contracts/config.md in internal/config/config.go
- [X] T028 [P] Implement secret resolution from env, `api_key_file`, `api_key_cmd`, and the keyring, never logging the value, in internal/config/secret.go
- [X] T029 [P] Write table tests for precedence, env aliases (HEADSCALE_CLI_*), and that secrets never appear in `fmt` output, in internal/config/config_test.go
- [X] T030 [P] Implement clipboard with an OSC52 fallback when SSH_TTY is set in internal/clip/clip.go
- [X] T031 [P] Implement unicode, nerd, and ascii icon sets in internal/icons/icons.go

### Theme engine

- [X] T032 Port viaduct's internal/theme (61 themes, Slug, Light, Set, ByName, All, SparkGradient) to internal/theme/{theme,themes}.go
- [X] T033 Add the Roles derivation and colour-depth fallback (truecolor, 256, 16, NO_COLOR) to internal/theme/roles.go
- [X] T034 [P] Add a user theme loader for TOML and Base16 YAML, with validation and the 4.5:1 contrast warning, per contracts/theme-file.md, in internal/theme/user.go
- [X] T035 [P] Write tests for the loader: valid file, missing key, bad hex, YAML scheme, and role override, in internal/theme/user_test.go
- [X] T036 Build `styles.Styles` from Roles, and rebuild it on theme change, in internal/ui/styles/styles.go

### UI primitives

- [X] T037 [P] Pane with a labelled rounded border and focus state (port of viaduct's helper) in internal/ui/pane/pane.go
- [X] T038 [P] Virtualised table with column priority, sort, multi-select, cursor stability across refreshes, and ellipsis truncation in internal/ui/table/table.go
- [X] T039 [P] Table tests: 10k rows render only the visible ones, the cursor stays on the same ID after a refresh, and columns drop by priority at narrow widths, in internal/ui/table/table_test.go
- [X] T040 [P] Filter query parser: free text, `tag:`, `user:`, `is:online|offline|expired|exit|pending`, `-negation`, and fuzzy mode, in internal/ui/filter/parse.go, plus a fuzz test in parse_fuzz_test.go
- [X] T041 [P] Form component with text, toggle, select, duration, and tag-list fields and validation messages in internal/ui/form/form.go
- [X] T042 [P] Confirm modal with three Danger levels; Critical requires typing the target name. In internal/ui/confirm/confirm.go
- [X] T043 [P] Toast stack (info, success, warn, error, with auto-dismiss) in internal/ui/toast/toast.go
- [X] T044 [P] Header, statusbar (key hints from Actions), sidebar (badges), tabs, and help overlay in internal/ui/{header,statusbar,sidebar,tabs,help}/
- [X] T045 [P] kv list, badge/chip, spinner, empty-state, and sparkline components in internal/ui/{kv,badge,spinner,empty,sparkline}/

### App shell and modularity

- [X] T046 Module, Action, Context, Danger, Scope, Modes, and Needs types per contracts/module.md in internal/module/module.go
- [X] T047 Registry with Register, All, ordering, and config-disabled modules in internal/modules/registry/registry.go, plus the blank-import aggregator internal/modules/all/all.go
- [X] T048 Store holding a cached snapshot, a polling refresh loop with active and idle intervals, and targeted invalidation, in internal/app/store.go
- [X] T049 Action pipeline (enabled, then read-only, then cap gate, then params form, then confirm, then run, then toast, activity, and refresh) in internal/app/actions.go
- [X] T050 [P] Pipeline tests proving that every Mutating action with Danger≥Confirm cannot run without confirmation, and that read-only blocks every Mutating action, in internal/app/actions_test.go (SC-005)
- [X] T051 Root model: layout (sidebar or tabs breakpoints, detail split), modal stack, global keys, resize handling, and a "resize me" notice below 80x24, in internal/app/app.go
- [X] T052 [P] Keymap that merges `[keys]` overrides and detects conflicts, in internal/keys/keys.go
- [X] T053 [P] Activity logger writing redacted JSONL to $XDG_STATE_HOME/headsup/activity.jsonl in internal/activity/activity.go
- [X] T054 Entry point cmd/headsup/main.go: parse flags, dispatch to cli.Run or the TUI, and wire up `--demo` to fake.Backend
- [X] T055 [P] Golden test harness: render each module at 80x24 and 120x40 with tokyo-night, solarized-light, and an ascii/16-colour profile, in internal/app/golden_test.go. Also add an all-themes smoke test in internal/app/themes_smoke_test.go: render every module in every bundled theme at 80x24 and assert no panic, no unset role, and no line wider than the terminal (SC-006).

**Checkpoint**: `headsup --demo` opens an empty shell with a working sidebar, theme, toasts, and
quit.

---

## Phase 2H: Host Foundation (blocks host-mode stories; API-only stories may proceed without it)

- [X] T056 Define the Host interface and the Env, File, Change, Effect, Perms, and error types (ErrChangedOnDisk, ErrInvalid, ErrPermission{Need, FixCommand}, ErrUnsupportedShape) per contracts/host.md in internal/host/host.go
- [X] T057 [P] In-memory host/fake with injectable permission errors, changed-on-disk conflicts, configtest failures, and an unhealthy restart, in internal/host/fake/fake.go
- [X] T058 Detection: use headscale's config search order (flag, then HEADSCALE_CONFIG, then /etc/headscale, then $HOME/.headscale, then .), plus version, local transport choice, service manager, log source, database kind, debug address, policy mode, and per-capability Perms with fix commands. In internal/host/detect/detect.go.
- [X] T059 [P] Table tests for detection over fixture filesystems: deb install, root vs member of the `headscale` group vs an outsider, a docker bind mount, and no headscale. In internal/host/detect/detect_test.go.
- [X] T060 Managed-file pipeline in internal/host/files/files.go:
  - sha256 at open, re-checked before writing;
  - backup with manifest (path, sha256, uid, gid, mode);
  - temp copy, then validators, then diff;
  - atomic write keeping uid, gid, and mode, with fsync of the file and its directory;
  - rollback.
  Backups use mode 0700 directories and 0600 files, and an insecure backup root is refused (FR-064); this logic is shared with internal/host/backup.
- [X] T061 [P] Pipeline tests: a failed validation leaves the target byte-identical; changed-on-disk returns ErrChangedOnDisk; owner and mode are preserved; rollback restores the exact bytes. In internal/host/files/files_test.go (SC-010).
- [X] T062 hsconfig typed model over a yaml.v3 Node tree: get and set by dotted key, insertion next to related keys, and `unix_socket_permission` always written as a quoted string. In internal/host/hsconfig/model.go.
- [X] T063 [P] Golden round-trip tests: editing one key changes exactly one line and keeps every comment, over testdata/hsconfig/{v029-example,v030-example,hand-written}.yaml, in internal/host/hsconfig/model_test.go
- [X] T064 Versioned field catalog for 0.29 and 0.30, giving each key's section, type, default, effect, danger, help text, deprecation, and replacement (for example `tls_cert_path` → `tls.cert_path`), in internal/host/hsconfig/catalog.go
- [X] T065 Service adapters (systemd, OpenRC, docker/podman, bare process, none), each with Status, Start, Stop, Restart, Reload, and Configtest. No sudo: permission failures return a FixCommand. In internal/host/service/*.go.
- [X] T066 [P] Log sources (`journalctl -o json -f`, `docker|podman logs -f`, file tail) and a line parser for headscale JSON and zerolog text that extracts node references, in internal/host/logs/*.go, plus a fuzz test in parse_fuzz_test.go
- [X] T067 [P] Debug and metrics client for /debug/{registration-cache,routes,filter,derp,ping,overview,config} and /metrics (expfmt), parsing defensively into ErrUnsupportedShape, in internal/host/debug/debug.go. Tests replay fixtures in testdata/debug/{v029,v030}/. Record the fixtures from real servers with scripts/record-debug-fixtures.sh, which runs headscale 0.29.6 and main in docker with seeded nodes, routes, and a pending web-auth registration. Do not hand-write them.
- [X] T068 [P] Read-only database enrichment in internal/host/db/db.go:
  - SQLite via modernc with `mode=ro` and `query_only`, and Postgres via pgx with `default_transaction_read_only`.
  - Reads `nodes.host_info` and `nodes.endpoints` into NodeExtras.
  - A migration-ID guard turns the feature off when the schema is unknown.
- [X] T069 [P] Lint tests: forbid INSERT, UPDATE, DELETE, ALTER, CREATE, DROP, and REPLACE in internal/host/db, and forbid running sudo, su, doas, or pkexec anywhere in internal/. In internal/host/lint_test.go.
- [X] T070 Store merge: attach NodeExtras, primary-route state, and registration-cache entries to the cached snapshot when a host is present, in internal/app/store.go
- [X] T071 App mode wiring in internal/app/modes.go:
  - run Detect at startup and create a `host:<hostname>` profile automatically;
  - honour `--host`, `--remote`, and `[host].enabled`;
  - dim host-only modules in remote mode, with an explainer view;
  - add a `Needs` gate in the action pipeline that shows the FixCommand.
- [X] T072 [P] Test that every Action with Needs is disabled, with a FixCommand reason, when Perms lacks it, and that host modules render their explainer in remote mode, in internal/app/modes_test.go

**Checkpoint**: `headsup --demo --host` shows host-mode chrome over host/fake, and on a real
host Detect reports the environment correctly through `headsup host info`.

---

## Phase 3: User Story 1, Connect and See My Tailnet (P1). MVP.

**Goal**: Connect through flags, environment variables, or a profile, and see a health
dashboard.

**Independent Test**: The dashboard's counts match fake and live servers. A bad key gives a
clear error and opens the connection editor.

- [X] T073 [P] [US1] teatest flow for start with an unauthorized key: an error toast appears and the connection editor opens, in internal/app/connect_test.go
- [X] T074 [P] [US1] Golden snapshots of the dashboard at both sizes in internal/modules/dashboard/testdata/
- [X] T075 [US1] Connection editor form (url, key source, CA, insecure warning, read-only) that saves to a profile, in internal/app/connect.go
- [X] T076 [US1] Profile switcher modal (`P`) that reconnects without restarting, in internal/app/profiles.go
- [X] T077 [US1] Dashboard module, made of cards:
  - Machines online/total, with a 1h sparkline of the online count.
  - Users.
  - Pending routes.
  - Pending registrations.
  - Expiring keys and nodes.
  - Server version and health.
  - An "issues" list that jumps to targets.

  In internal/modules/dashboard/dashboard.go.
- [X] T078 [US1] Version support check: a warning banner for unsupported versions, in internal/app/connect.go
- [X] T079 [US1] Host-mode start: zero flags, connecting through the detected local transport with no key, a "host:<name>" header, and a permission explainer with fix commands plus a "use remote mode" option, in internal/app/connect.go
- [X] T080 [P] [US1] teatest flows for host start as a member of the headscale group, as an outsider (the explainer appears), and with `--remote`, over host/fake, in internal/app/hostconnect_test.go. Include a startup budget test: with host/fake simulating realistic subprocess and socket latency (Detect around 150ms, first list around 300ms), the first dashboard frame must render in under 3s, and the test fails over budget (SC-009). Run the same check on real hardware in the integration job.
- [X] T081 [US1] Live metric cards on the dashboard (nodes, map responses per second, memory) from ring-buffered Metrics samples, in internal/modules/dashboard/metrics.go

---

## Phase 4: User Story 2, Manage Machines (P1)

**Goal**: Full machines page parity.

**Independent Test**: With 50 fake nodes, filter, rename, bulk re-tag three, and delete one.
The fake records exactly those calls.

- [X] T082 [P] [US2] teatest flow covering the filter, rename, bulk tag, and delete scenario in internal/modules/machines/flow_test.go
- [X] T083 [P] [US2] Golden snapshots of the list, the detail split, and the narrow layout in internal/modules/machines/testdata/
- [X] T084 [US2] Machines list view with configurable columns (`[ui.columns].machines`), status dots, tag chips, relative times, and sort keys `s`/`S`, in internal/modules/machines/list.go
- [X] T085 [US2] Machine detail pane: IPs, tags, available, approved, and served routes, expiry, last seen, created, register method, user, pre-auth key, and short key fingerprints. In internal/modules/machines/detail.go.
- [X] T086 [US2] Actions in internal/modules/machines/actions.go:
  - Rename, validating "DNS label: lowercase, [a-z0-9-], 1–63 chars" and suggesting a sanitized name.
  - Tags, validating `^tag:[a-z0-9-]+$` and sending the full replacement list.
  - Expire, and disable expiry where `cap` allows.
  - Delete, at Danger=Critical.
  - Copy IP and copy name.
- [X] T087 [US2] Multi-select with space and ctrl+a, plus bulk tag (add or remove, merged per node), bulk expire, and bulk delete, with a confirm that lists the targets. In internal/modules/machines/bulk.go.
- [X] T088 [US2] Show "Change owner" in help as unsupported by headscale, rather than leaving it out silently, in internal/modules/machines/actions.go
- [X] T089 [US2] Keep the cursor and selection by node ID across auto-refresh, in internal/modules/machines/list.go
- [X] T090 [US2] Host columns (os, client, derp, endpoints) and a detail section from NodeExtras, hidden in remote mode, in internal/modules/machines/{list,detail}.go
- [X] T091 [US2] Ping action `p` through Debug().Ping, with Needs=DebugAccess, in internal/modules/machines/actions.go

---

## Phase 5: User Story 3, Add a Device (P1)

**Goal**: Pending queue, register by key or URL, and pre-auth key creation with a copyable
command and a QR code.

**Independent Test**: A reusable tagged key reaches the backend with the right options, and the
command contains the right login server and key.

- [X] T092 [P] [US3] Table tests for extracting the registration key or auth ID from a pasted URL (`/register/<key>`, `/auth/<id>`, a bare key, and whitespace or newline noise) in internal/modules/pending/parse_test.go
- [X] T093 [P] [US3] teatest flow: create a pre-auth key, see it shown once, then see it masked in the list, in internal/modules/pending/flow_test.go
- [X] T094 [US3] Pending module: paste-to-queue, approve as a user, reject, and register by key, in internal/modules/pending/pending.go
- [X] T095 [US3] New pre-auth key form (user, reusable, ephemeral, expiration, tags) with a one-time reveal screen, the `tailscale up --login-server=<url> --authkey=<key>` command, copy, and QR. In internal/modules/pending/newkey.go.
- [X] T096 [P] [US3] Half-block QR renderer in internal/ui/qr/qr.go
- [X] T097 [US3] Fill the pending queue automatically from /debug/registration-cache in host mode, merged with pasted entries, in internal/modules/pending/pending.go. **Finding:** real headscale (0.29.6 and main) exposes only the cache size there, so host mode shows the count of waiting logins and keeps the entry merge for servers that list them

**Checkpoint (MVP)**: US1, US2, and US3 work end to end on the fake backend and on headscale
0.29.6.

---

## Phase 6: User Story 4, Users (P2)

- [X] T098 [P] [US4] Test that delete is blocked while NodeCount > 0 and offers "show machines", in internal/modules/users/users_test.go
- [X] T099 [US4] Users module: list (name, display name, email, provider, created, machine and key counts), create, rename, and delete, in internal/modules/users/users.go
- [X] T100 [US4] Navigation to machines filtered by `user:<name>` and to keys filtered by user, through Context.Navigate, in internal/modules/users/users.go

## Phase 7: User Story 5, Access Controls (P2)

- [X] T101 [P] [US5] Policy parser tests with comments, trailing commas, all sections, and byte-for-byte round trips, in internal/policy/parse_test.go, plus a fuzz test
- [X] T102 [P] [US5] Diff tests: adding one rule yields a one-hunk diff, in internal/policy/diff_test.go
- [X] T103 [US5] HuJSON parse into a Document (groups, tagOwners, hosts, acls, grants, ssh, autoApprovers, tests) in internal/policy/document.go
- [X] T104 [US5] Line diff in internal/policy/diff.go, plus the diffview component in internal/ui/diffview/diffview.go
- [X] T105 [P] [US5] chroma codeview with a theme-derived style, line numbers, and an error-line marker in internal/ui/codeview/codeview.go
- [X] T106 [US5] Policy history store (last 50 per profile, sha256 dedupe) in internal/policy/history.go
- [X] T107 [US5] Policy module viewer, inline editor (bubbles/textarea), and `$EDITOR` round-trip via tea.ExecProcess, in internal/modules/policy/{view,edit}.go
- [X] T108 [US5] The apply flow, in internal/modules/policy/apply.go:
  1. CheckPolicy, or a local parse when the check is unavailable.
  2. Show the error with line and column, keeping the buffer.
  3. Show the diff.
  4. Run the `updatedAt` conflict check.
  5. Confirm.
  6. SetPolicy.
  7. Record history.

  When the server is in file mode, explain that the policy is view-only.
- [X] T109 [US5] Explorer: a tree of groups, tags, hosts, and rules with "where used" cross-links, in internal/modules/policy/explore.go
- [X] T110 [US5] Rollback from history with a diff and confirm, in internal/modules/policy/history.go
- [X] T111 [US5] File-mode policy apply in host mode through the managed-file pipeline, with `headscale policy check` as the validator, then Service().Reload(), then a refetch to confirm the policy is active, with rollback on failure. In internal/modules/policy/apply.go.
- [X] T112 [P] [US5] Tests for file mode: invalid → file untouched; valid → written, reload called, active; reload failure → rollback offered. In internal/modules/policy/filemode_test.go.
- [X] T113 [US5] Compiled filter view (host) from /debug/filter, next to the access preview, in internal/modules/policy/filter.go

## Phase 8: User Story 6, Routes and Exit Nodes (P2)

- [X] T114 [P] [US6] Test that approving one prefix sends the full approved list with the prefix added and nothing else changed, in internal/modules/routes/routes_test.go
- [X] T115 [US6] Routes module: group by prefix (HA sets), show approved and served state, a separate exit-node section, overlap highlighting, and approve, revoke, and approve-all. In internal/modules/routes/routes.go.
- [X] T116 [US6] Sidebar badge with the count of pending routes in internal/modules/routes/routes.go
- [X] T117 [US6] Primary and failover markers for HA sets from /debug/routes in host mode, in internal/modules/routes/routes.go

## Phase 9: User Story 7, Keys (P2)

- [X] T118 [P] [US7] Test that expiring the API key matching the active profile's prefix triggers the self-lockout warning, in internal/modules/keys/keys_test.go
- [X] T119 [US7] Keys module with pre-auth and API tabs: list, create, expire, and delete (delete gated by cap), with masked secrets and API keys addressed by prefix. In internal/modules/keys/keys.go.
- [X] T120 [US7] Expiring badge and an "expires in" colour ramp in internal/modules/keys/keys.go

## Phase 10: User Story 8, Look and Feel (P2)

- [X] T121 [P] [US8] teatest flow: switch theme in the picker, check every view re-renders, check the choice persists to config, and check a user theme appears, in internal/modules/prefs/theme_test.go
- [X] T122 [US8] Theme picker modal (`T`) with live preview, light/dark filter, search, and reload of user themes, in internal/ui/themepicker/themepicker.go
- [X] T123 [US8] Prefs module (Headsup's own settings) covering theme, icons, density, sidebar mode, columns editor, refresh intervals, and time format, in internal/modules/prefs/prefs.go
- [X] T124 [US8] Per-profile theme override in internal/app/profiles.go
- [X] T125 [P] [US8] Ship example themes in themes/ (Midnight Harbor plus a high-contrast accessibility theme)

## Phase 11: User Story 9, Command Palette and Navigation (P2)

- [X] T126 [P] [US9] Test that every registered Action with Keys is present in the palette and the help screen, in internal/app/palette_test.go
- [X] T127 [US9] Fuzzy command palette (`:`, `ctrl+p`) over all Actions plus "go to <module>" and "theme: <name>", with recent items first, in internal/ui/palette/palette.go
- [X] T128 [US9] Context-aware help overlay generated from Actions and the keymap, in internal/ui/help/help.go
- [X] T129 [US9] Optional mouse support: click rows, scroll, and click sidebar entries, in internal/app/mouse.go

## Phase 12: User Story 10, Server Settings Editor (P2) [host]

**Independent Test**: Change `node.expiry` in a commented sample config. The diff shows one
line, comments survive, and a backup exists. An invalid value is refused before writing.

- [X] T130 [P] [US10] teatest flow: edit a field, see the diff, confirm, see the restart offer, and on an unhealthy restart get a restore offer, over host/fake, in internal/modules/serverconfig/flow_test.go
- [X] T131 [US10] Server settings module with section navigation (General, Nodes, DNS→link, DERP→link, TLS, OIDC, Policy, Database (view), Logging, Features). Each field row shows value, default, effect badge, and help, driven by the catalog. In internal/modules/serverconfig/serverconfig.go.
- [X] T132 [US10] Field editors by catalog type (duration, enum, list, map, path, secret masked, quoted octal), with validation messages, in internal/modules/serverconfig/fields.go
- [X] T133 [US10] Apply flow: run the pipeline with `headscale configtest -c <tmp>`, show the diff, confirm according to field Danger, write, then offer a restart, with a post-restart health check and log tail and a restore offer if unhealthy. In internal/modules/serverconfig/apply.go.
- [X] T134 [US10] `$EDITOR` escape hatch through tea.ExecProcess, going through the same pipeline, plus a three-way diff when the file changed on disk, in internal/modules/serverconfig/editor.go
- [X] T135 [US10] Deprecated-key detection and one-key migration (`m`), for example 0.29's flat TLS keys → 0.30's `tls.*`, in internal/modules/serverconfig/migrate.go

## Phase 13: User Story 13, DNS (P2) [host]

**Independent Test**: Add an A record. The file is written atomically and resolves from a
client without a restart.

- [X] T136 [P] [US13] Model and validator tests: Record Type is one of A or AAAA; the Value family matches the type; BaseDomain differs from the server_url host; extra_records and extra_records_path are mutually exclusive. In internal/host/dnsrec/dnsrec_test.go.
- [X] T137 [US13] Extra-records file model (keeping its existing JSON, YAML, or HuJSON format) and inline-records model in internal/host/dnsrec/dnsrec.go
- [X] T138 [US13] DNS module: MagicDNS, base domain, override local DNS, global nameservers, split DNS (domain → servers), search domains, and a records table (add, edit, delete), with effect badges (records are live, everything else needs a restart). In internal/modules/dns/dns.go.
- [X] T139 [US13] "Move inline records to a file" action that creates the file and updates `dns.extra_records_path` in one confirmed change set, in internal/modules/dns/migrate.go

## Phase 14: User Story 14, Service and Logs (P2) [host]

**Independent Test**: With a fake service and log source: filter to errors, pause, and resume.
A restart with an invalid config is refused before the service is touched.

- [X] T140 [P] [US14] teatest flow: logs follow, level filter, pause and resume, restart and start refused on a configtest failure, and stop requiring a typed hostname, in internal/modules/service/flow_test.go
- [X] T141 [US14] Service status panel (state, since, PID, memory, version) with start, stop, restart, and reload actions. Danger levels:
  - Stop is Critical (type the hostname) and warns that every client loses the control plane.
  - Restart is Confirm, with a "clients will reconnect" note, and runs configtest first.
  - Start runs configtest first and is refused if it fails.
  - Reload is Confirm and notes that it reloads the policy only. In internal/modules/service/status.go.
- [X] T142 [US14] Log viewer: a virtualised ring buffer of 10k lines, level filter, regex search, pause, jump to next error, structured-field expansion, and a node link to machines, in internal/modules/service/logs.go
- [ ] T143 [US14] Dashboard and statusbar service badge (● running, ○ stopped, ◐ restarting) in internal/modules/service/badge.go

## Phase 15: User Story 11, Scriptable CLI (P3)

- [ ] T144 [P] [US11] Golden tests for CLI output in table, json, and yaml formats, plus exit-code tests, in internal/cli/cli_test.go
- [ ] T145 [US11] Subcommand router and formatters per contracts/cli.md in internal/cli/{router,format}.go
- [ ] T146 [US11] Verbs for nodes, routes, users, keys, policy, auth, server, themes, config, and version, reusing the module Actions' Run logic, in internal/cli/cmd_*.go
- [ ] T147 [US11] TTY-aware confirmation, with `--yes` and exit code 7 when not interactive, in internal/cli/confirm.go
- [ ] T148 [US11] Host verbs (`host`, `svc`, `conf`, `dns`, `derp`, `backup`, `upgrade`), reusing module Actions, with exit codes 8 (no host) and 9 (permission) and the fix command on stderr, in internal/cli/cmd_host.go

## Phase 16: User Story 12, Activity and Watch (P3)

- [ ] T149 [P] [US12] Snapshot differ tests (online, offline, key expiring, route advertised) in internal/watch/watch_test.go
- [ ] T150 [US12] Snapshot differ emitting events in internal/watch/watch.go
- [ ] T151 [US12] Activity module: a filterable log of this app's mutations plus a live event feed, with export to JSON, in internal/modules/activity/activity.go
- [ ] T152 [US12] Record host file writes and service actions in the activity log, with backup ID and diff stats and secrets redacted, in internal/activity/activity.go
- [ ] T153 [US12] Desktop and bell notifications driven by `[notify]` in internal/app/notify.go
- [ ] T154 [US12] Access preview: expand groups, tags, and autogroups, evaluate acls and grants for a chosen source, and label the result best-effort. In internal/policy/preview.go, with the view in internal/modules/policy/preview.go and table tests in internal/policy/preview_test.go.

## Phase 17: User Story 15, DERP (P3) [host]

- [X] T155 [P] [US15] DERP map model and validator tests (custom region ID 1–999, unique codes, ports, at least one node per region) in internal/host/derpmap/derpmap_test.go
- [X] T156 [US15] DERP map file model (json, yaml, or hujson, keeping the format) in internal/host/derpmap/derpmap.go
- [X] T157 [US15] DERP module: an embedded server form (`derp.server.*`), a URLs and paths list, a region and node editor for map files, a live map from /debug/derp, and per-region machine preference and latency from NodeExtras. In internal/modules/derp/derp.go.

## Phase 18: User Story 16, Server Health, Backup, and Upgrades (P3) [api+host]

- [ ] T158 [US16] Server module: version, health, API flavour, a capability matrix table, config-file-only explanations with doc links, backfill IPs at Danger=Critical, and "open local config" when the config is found on disk. In internal/modules/server/server.go.
- [ ] T159 [P] [US16] Doctor tests over host/fake: an expired certificate, a deprecated key, no socket permission, WAL growth, and an unreachable server_url, each producing a Finding with a Fix. In internal/host/doctor/doctor_test.go.
- [ ] T160 [US16] Doctor checks: permissions, socket, server_url reachability, TLS certificate expiry (from cert files and the ACME cache), deprecated keys, database and WAL size, disk space, and clock skew. In internal/host/doctor/doctor.go. Use the spec US16 default thresholds, configurable under `[host.doctor]`.
- [ ] T161 [US16] Backup bundle in internal/host/backup/backup.go:
  - the config, policy file, extra records, DERP files, and noise and DERP keys;
  - the database copied with the SQLite online backup API, or pg_dump instructions for Postgres;
  - a manifest, retention, and restore for files only, with database restore given as steps;
  - directories created with mode 0700 and files with 0600 under a 0077 umask;
  - refusing an insecure backup root with ErrInsecureBackupDir and a fix command;
  - manifests holding only paths, hashes, ownership, and mode (FR-064).
- [ ] T162 [P] [US16] Backup tests, in internal/host/backup/backup_test.go:
  - the manifest hashes match, restore is byte-exact, and the database is never written;
  - created directories and files are 0700 and 0600;
  - a 0755, symlinked, or foreign-owned backup root is refused;
  - no secret bytes (the noise key or the OIDC secret) appear in the manifest or the activity log.
- [ ] T163 [US16] Upgrade assistant: latest release vs installed version, enforcing one minor version at a time with no downgrade and 0.30 only from 0.29.x, rendering release notes, offering a backup first, and printing package-manager commands without running them. In internal/host/upgrade/upgrade.go.
- [ ] T164 [US16] Server module UI: capability matrix by mode, doctor list with fix actions, backups list, and upgrade card, in internal/modules/server/{doctor,backup,upgrade}.go

---

## Phase 19: Polish and Launch

- [ ] T165 [P] README in viaduct style: centred header and badges, a VHS demo GIF (demo.tape using `--demo`), feature list, comparison table against web UIs, install for brew, scoop, deb, and go install, quick start, keys, and a collapsible themes gallery
- [ ] T166 [P] docs/THEMES.md (authoring guide and role table), docs/MODULES.md (how to add a module in one directory), and CONTRIBUTING.md
- [ ] T167 [P] docs/CAPABILITIES.md: a capability matrix generated from the endpoint table, covering parity with the Tailscale console and headscale gaps
- [ ] T168 [P] docs/HOST-MODE.md: permissions model (root vs the `headscale` group), container setup, what each managed file does and when changes take effect, and the backup and restore story
- [ ] T169 [P] Integration test job against dockerised headscale 0.29.6 and main: run quickstart V1–V12 in remote mode, and H1–H11 in host mode by running headsup inside the headscale container image, in test/integration/ Add a shape-drift check that re-records the /debug/* and /metrics responses from both images and fails if the parsers return ErrUnsupportedShape. Schedule it weekly against headscale main.
- [ ] T170 Performance check: 2,000-node fake, filter keystroke to frame time under 16ms, as a benchmark in internal/ui/table/bench_test.go
- [ ] T171 Accessibility pass: the ascii icon set, and a 16-colour plus NO_COLOR run of every golden test
- [ ] T172 Run the quickstart.md validation end to end and update CHANGELOG.md for v0.1.0
- [X] T173 Ask-first restart under sudo when host mode lacks access (constitution 2.0.0, US1 scenario 3), in cmd/headsup/elevate.go, with the escalation lint extended to cmd/ in internal/host/lint_test.go

---

## Dependencies and Execution Order

```text
Setup (T001–T010)
  └─▶ Foundational (T011–T055)  ── blocks everything
        ├─▶ Host Foundation (T056–T072) ── blocks host-only stories and host tasks inside api+host stories
        │
        ├─▶ US1 Connect/Dashboard ─┐
        ├─▶ US2 Machines           ├─▶ MVP checkpoint (v0.1.0-alpha, host + remote)
        ├─▶ US3 Add device         ┘
        ├─▶ US14 Service & Logs  US13 DNS  US10 Server settings   (host; independent)
        ├─▶ US4 Users   US6 Routes   US7 Keys                       (independent)
        ├─▶ US5 Policy ──▶ US12 access preview (T154)
        ├─▶ US8 Look&Feel   US9 Palette                             (independent)
        ├─▶ US15 DERP   US16 Health/Backup/Upgrade                  (host)
        └─▶ US11 CLI (needs Actions from the modules it exposes)
              └─▶ Polish (T165–T172)
```

- API-only stories (US4, US7) and the API parts of api+host stories depend only on
  Foundational.
- Host-only stories (US10, US13, US14, US15) and the host tasks inside US1, US2, US3, US5, US6,
  and US16 also need Host Foundation.
- Other cross-story dependencies:
  - T154 needs US5.
  - US11 needs the Actions of the modules it exposes.
  - US10, US13, US15, and the file-mode tasks in US5 all reuse the managed-file pipeline
    (T060–T061).
- Within a story, test tasks come before implementation.

## Parallel Examples

- **Foundational**: T037 through T045 (the UI primitives) can all run together. T015, T017, and
  T018 (contract suite and fixtures) can run alongside T026 through T031.
- **Host Foundation**: after T056 and T057, the service, logs, debug, and database tasks
  (T065, T066, T067, T068) can run in parallel. The file pipeline (T060), the hsconfig model
  (T062), and the field catalog (T064) form one chain.
- **After the foundations**: up to five developers can work without file overlap, one each on
  US2 (machines), US3 (pending), US5 (policy), US14 (service), and US13 (DNS).
- **US5**: T101, T102, and T105 together, then T103 → T104 → T107 → T108.

## Implementation Strategy

1. **MVP (v0.1.0-alpha)**: Setup, Foundational, Host Foundation, US1, US2, and US3. Running
   `headsup` on the server with no flags gives a live tailnet with OS and DERP detail, and the
   pending queue fills itself. Ship it with `--demo`.
2. **The host killer features (v0.2)**: US14 (service and logs), US13 (DNS), and US10 (server
   settings). None of these is possible in any headscale web UI without a server agent.
3. **Admin parity (v0.3)**: US4 through US7. Policy works in both database and file mode.
4. **Delight (v0.4)**: US8 and US9, with themes and the palette. Invite theme PRs.
5. **Ops (v0.5)**: US11, US12, US15, and US16, adding the CLI, watch, access preview, DERP,
   doctor, backups, and the upgrade assistant.
6. **v1.0**: Polish, docs (including HOST-MODE.md), packaging, and an announcement in
   headscale's community-integrations list.
