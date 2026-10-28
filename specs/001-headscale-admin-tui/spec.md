# Feature Specification: Headsup, a Headscale Admin TUI

**Feature Branch**: `001-headscale-admin-tui`

**Created**: 2026-10-28

**Status**: Draft

**Input**: User description: "Build an amazing TUI for headscale. Plan out as much functionality
as you can, replacing things you could normally do via the Tailscale web UI. Make it modular,
pretty, and themeable (like viaduct and flo), and give the community something to love."
Follow-up: "I mean for this TUI to run on the box running headscale, so it might be able to
directly edit files, not just use the API. That could expose more features."

## Operating Modes

- **Host mode (primary)**: Headsup runs on the headscale server itself. It finds headscale's
  config, connects locally without an API key, and can also:
  - read and edit headscale's files: config, policy file, DNS extra records, and DERP map;
  - control the service and read its logs;
  - read live metrics and debug data;
  - read extra machine detail from the database, read-only.
- **Remote mode**: Headsup runs anywhere and uses the API with a key. It covers every API
  feature. Host-only features appear with a "available when run on the server" note.
- Each user story below is marked **[api]**, **[host]**, or **[api+host]** to show which mode
  it needs. [api+host] means it works in both modes and does more in host mode. **[local]**
  means a client-side feature that works the same in every mode.

## User Scenarios & Testing *(mandatory)*

Personas:

- **Homelab admin**: one person running headscale for their own devices.
- **Team operator**: runs headscale for a small organization with several users, tags, and an
  ACL policy.
- **On-call responder**: SSHed into a box at 2am, needs to find and fix one thing fast.

### User Story 1 - Connect and See My Tailnet (Priority: P1) [api+host]

On the server, the admin types `headsup` with no flags. It finds headscale's config, connects
locally, and opens the dashboard. Remotely, the admin gives a server address and API key, or a
saved profile.

Within a second the dashboard shows:

- how many machines are online and offline, and how many users;
- which keys or nodes expire soon;
- routes and registrations waiting for approval;
- the server version and service state.

In host mode the dashboard also shows live server metrics as sparklines: connected nodes,
map-response rate, and memory.

**Why this priority**: Nothing else works without connecting, and the dashboard alone answers
"is my tailnet healthy?"

**Independent Test**: Point the app at a headscale server (or a fake) and confirm the dashboard
numbers match what the server reports.

**Acceptance Scenarios**:

1. **Given** a valid address and key, **When** the app starts, **Then** the dashboard shows
   machine, user, route, and expiry counts plus the server version.
2. **Given** the app runs on the headscale host as a user allowed to reach the local socket,
   **When** it starts with no flags, **Then** it connects with no API key and the header shows
   "host mode".
3. **Given** host mode but a user without permission on the socket or files, **When** it starts,
   **Then** it explains exactly which permission is missing. It shows the command to fix it
   (for example, add the user to the `headscale` group, or rerun with sudo) and offers remote
   mode.
4. **Given** an unreachable server or a rejected key, **When** the app starts, **Then** a clear
   error says which one it was and how to fix it, and offers to edit the connection.
5. **Given** several saved profiles, **When** the user opens the profile switcher, **Then** they
   can move to another server without restarting.
6. **Given** a server version outside the supported range, **When** connecting, **Then** the
   app warns which features may not work.

---

### User Story 2 - Manage Machines (Priority: P1) [api+host]

This mirrors the console's Machines page. The admin browses every machine in a sortable,
filterable table:

- Columns: name, owner, Tailscale IPs, tags, online/last seen, expiry, registration method, and
  route status.
- In host mode, extra columns are available: OS, client version, preferred DERP region and
  latency, and endpoints.
- Opening a machine shows full detail. In host mode the admin can also ping the machine from
  the server.
- Actions on a machine: rename, edit tags, expire (force re-auth), disable key expiry where
  the server allows it, approve routes or exit-node use, and delete. Changing a machine's owner
  is not possible through the headscale API; the UI says so and does not offer it.
- Several machines can be selected for bulk tag, expire, or delete.

**Why this priority**: Machines are the most-used page of the web console.

**Independent Test**: Against a fake backend with 50 machines, filter by tag, rename one,
re-tag three in bulk, and delete one. The backend sees exactly those calls.

**Acceptance Scenarios**:

1. **Given** the machine list, **When** the user types `/` and a query, **Then** the list
   narrows live. The query matches name, IP, user, and tag; tokens like `tag:server`,
   `user:alice`, `online`, `offline`, `expired`, and `exit` refine it.
2. **Given** a selected machine, **When** the user chooses delete, **Then** a confirmation names
   the machine, and nothing happens unless they confirm.
3. **Given** a machine with advertised but unapproved routes, **When** the user opens it,
   **Then** each route is listed with its state and can be approved or revoked one at a time.
4. **Given** the user copies an IP or name, **When** they press the copy key, **Then** it lands
   on the system clipboard (falling back to OSC52 over SSH).
5. **Given** machines change on the server, **When** the auto-refresh interval passes, **Then**
   the list updates without losing the cursor or selection.

---

### User Story 3 - Add a Device (Priority: P1) [api+host]

This mirrors the console's "Add device" button. A **pending approvals** queue lists devices
waiting on interactive login (the web-auth flow), each with an approve or reject action. In
host mode the app shows how many devices headscale is waiting on. headscale's debug cache counts
pending logins but does not list them, so in both modes the entries themselves come from what
the admin pastes. The admin can also add a device in one of two ways:

- **Registers** a pending machine by pasting the registration key or URL that the client
  printed, and picking the owner; or
- **Generates a pre-auth key**, choosing owner, reusable, ephemeral, expiry, and tags.

The app shows the exact `tailscale up` command for this server, ready to copy.

**Why this priority**: Onboarding devices is the second most common admin task.

**Independent Test**: Generate a reusable, tagged key and confirm the backend received those
options. The displayed command contains the right login server and key.

**Acceptance Scenarios**:

1. **Given** a device waiting in the pending queue, **When** the admin approves it as user
   `alice`, **Then** the device joins owned by alice and leaves the queue. Rejecting removes it
   without registering.
2. **Given** a registration URL pasted in full, **When** submitted, **Then** the app pulls out
   the key and registers the node to the chosen user.
3. **Given** a newly created pre-auth key, **When** shown, **Then** it is shown once in full with
   a copy action, and masked everywhere afterwards.

---

### User Story 4 - Users (Priority: P2) [api]

This mirrors the console's Users page. The admin lists users with display name, email,
provider (local or OIDC), created date, and machine count. They can create, rename, and delete
users, and jump from a user to that user's machines and keys. Deleting a user who still owns
machines is blocked, with an explanation and a shortcut to those machines.

**Why this priority**: Needed by teams; homelabs often have just one user.

**Independent Test**: Create a user, rename it, and attempt to delete it while it owns a
machine. The delete is blocked.

**Acceptance Scenarios**:

1. **Given** a user who owns machines, **When** delete is requested, **Then** it is blocked and
   the user is offered "show machines".

---

### User Story 5 - Access Controls (Policy) (Priority: P2) [api+host]

This mirrors the console's Access Controls page. The admin views the current policy with syntax
highlighting:

- **Edit**: in their `$EDITOR` or an inline editor. The edit is validated by the server
  without being applied, a diff against the current policy is shown, and nothing is applied
  until they confirm. Policy `tests` in the document are run as part of validation.
- **Explore**: a structured browser shows groups, tag owners, hosts, and rules, with
  cross-links (for example "which rules mention `tag:server`?").
- **Preview**: a "who can reach what" view takes a source user, tag, or machine and lists
  reachable destinations and ports, worked out from the policy.
- **History**: the previous policy is kept locally so a bad change can be rolled back.
- **Compiled filter** (host): shows the packet filter headscale actually compiled from the
  policy, so the preview can be checked against the server's own view.

**Why this priority**: The web console's policy editor is a key feature, and terminals are good
at text editing and diffs.

**Independent Test**: Load a sample policy, add a rule in the editor, and confirm the diff
shows only that rule. Applying it sends the new policy. A broken policy is refused with the
parser's line and column.

**Acceptance Scenarios**:

1. **Given** an invalid policy edit, **When** the user tries to apply it, **Then** it is refused
   with the location of the error, and the edit buffer is kept.
2. **Given** headscale reads its policy from a file and the app is in host mode, **When** the
   user applies an edit, **Then** the app does the following:
   1. Backs up the file.
   2. Validates the edit with headscale's policy checker.
   3. Shows a diff and asks for confirmation.
   4. Writes the file atomically.
   5. Signals headscale to reload.
   6. Confirms the new policy is active.

   If the reload fails, it offers a one-key rollback.
3. **Given** a policy file and remote mode, **When** the user opens the editor, **Then** the app
   explains that file-mode policies can only be edited on the host, and still offers viewing
   and exploring.

---

### User Story 6 - Routes and Exit Nodes (Priority: P2) [api+host]

This is a cross-machine view of all subnet routes and exit nodes: which machine advertises
each route, whether it is approved, and whether it is currently being served. When several
machines advertise the same prefix they are grouped as an HA set. The admin approves or revokes
from here. Overlapping prefixes are highlighted. In host mode, the current primary for each HA
set and its failover candidates are shown, using the server's own route state.

**Why this priority**: The web console spreads this across machine pages. One view is clearer.

**Independent Test**: With two machines advertising the same /24, the view groups them.
Approving one sends that machine's full approved-route list, with the new route added and
existing routes untouched.

---

### User Story 7 - Keys (Priority: P2) [api]

This mirrors the console's Settings → Keys page:

- **Pre-auth keys**: list with owner, reusable/ephemeral/used flags, tags, created, and
  expiry. Create, expire, and delete.
- **API keys**: list by prefix, created, expiry, and last seen. Create, which shows the key
  once; expire, which warns if it is the key the app itself is using; and delete. Keys are
  identified by prefix, not numeric ID, because IDs can change across server upgrades.
- **OAuth clients** (deferred to v1.1): list, create with scopes, and delete, on 0.30 and
  later. Until then, the capability matrix lists them as "planned".

**Independent Test**: Expire a pre-auth key and confirm it shows as expired. Trying to expire
the app's own API key triggers a specific warning.

---

### User Story 8 - Look and Feel (Priority: P2) [local]

The user picks from many built-in themes with live preview, or drops their own theme file into
the config directory. Their choice persists. Layout, density (compact or comfortable), icon set
(Unicode, Nerd Font, or ASCII), and which columns each table shows are all configurable.

**Independent Test**: Switch themes in the picker. Every screen re-renders in the new palette
with no restart. A user theme file appears in the picker.

---

### User Story 9 - Command Palette and Navigation (Priority: P2) [local]

`:` or `Ctrl+P` opens a fuzzy command palette that lists every action in every module, for
example "expire machine", "new pre-auth key", "switch theme", or "go to routes". A sidebar or
tab bar switches modules with number keys. `?` shows context-aware help. Key bindings can be
remapped in config.

**Independent Test**: Every action reachable by a key is also reachable from the palette. A
remapped key works and shows up in help.

---

### User Story 10 - Server Settings Editor (Priority: P2) [host]

A structured editor for headscale's main config file, grouped into sections:

- General: server URL, listen addresses, IP prefixes and allocation.
- Nodes: expiry, ephemeral inactivity timeout, HA probe settings.
- Database (view only).
- TLS and ACME.
- OIDC.
- Logging.
- Policy mode and path.
- Feature toggles.

Each field shows its current value, its default, and a short description. It is marked
"applies on save" or "needs restart". Saving does the following:

1. Keeps comments and layout.
2. Validates with headscale's config checker.
3. Shows a diff and asks for confirmation.
4. Writes atomically.
5. Offers to restart the service, with a warning that clients will reconnect.

Deprecated keys, such as the 0.30 move of TLS settings, are flagged, with a one-key migration.
Secrets such as the OIDC client secret are masked. Free-form `$EDITOR` editing goes through the
same validate, diff, and confirm pipeline.

**Why this priority**: Every one of these settings is config-file-only in headscale, and today
changing them means hand-editing YAML over SSH.

**Independent Test**: Against a sample config with comments, change the node expiry. The diff
shows one line, comments survive, a backup exists, and an invalid value is refused before
anything is written.

**Acceptance Scenarios**:

1. **Given** a change to a "needs restart" field, **When** saved, **Then** the app offers a
   restart. If headscale fails to come back healthy, it offers to restore the backup and
   restart again.
2. **Given** the config file changed on disk since it was opened, **When** saving, **Then** the
   app warns and shows a three-way diff instead of overwriting.

---

### User Story 11 - Scriptable Companion CLI (Priority: P3) [api+host]

Every TUI action also exists as a non-interactive subcommand with `--output json|table|yaml`,
so the same tool works in scripts and CI. Examples: `headsup nodes ls --filter tag:server -o
json` and `headsup keys preauth create --user ops --reusable`.

**Independent Test**: The JSON output of a list command parses and matches the TUI's data.

---

### User Story 12 - Activity and Watch (Priority: P3) [api+host]

The app keeps a local activity log of every change it made: who, what, when, and which
profile. A "watch" mode highlights machines coming online or going offline and keys about to
expire, and can show desktop or terminal-bell notifications.

**Independent Test**: Perform three actions. The activity log lists them in order with their
targets.

---

### User Story 13 - DNS (Priority: P2) [host]

This mirrors the console's DNS page:

- MagicDNS on or off, and base domain.
- Override of local DNS.
- Global nameservers.
- Split DNS: domain to nameservers.
- Search domains.
- Extra DNS records (name, type, value).

Extra records kept in their own file apply immediately, because headscale reloads that file by
itself. The app moves records from the main config into that file on request. Other DNS changes
are marked "needs restart".

**Why this priority**: DNS is one of the most-used console pages, and headscale offers no API
for it.

**Independent Test**: Add an A record in the extra-records editor. The file is updated
atomically, and resolving the name from a tailnet client works without a restart.

---

### User Story 14 - Service and Logs (Priority: P2) [host]

The admin sees the headscale service state: running or stopped, uptime, PID, memory, and
version. They can start, stop, restart, or reload it. A restart always runs a config check
first.

A live log viewer follows headscale's logs. It parses structured log lines and offers:

- a level filter;
- text search;
- pause;
- a jump to errors;
- links from a log line to the machine it mentions.

Log sources are detected: system journal, container logs, or a log file.

**Why this priority**: When something breaks, logs and restarts are the first two things an
admin reaches for.

**Independent Test**: With a fake service and log source, filter to errors, pause, and resume.
A restart with an invalid config is refused before the service is touched.

---

### User Story 15 - DERP (Priority: P3) [host]

The admin views and edits:

- the embedded DERP server settings: enabled, region ID, code, and name, STUN address, and
  client verification;
- the list of DERP map URLs and files;
- custom DERP map files, through a typed editor for regions and nodes.

A live view shows the server's current DERP map. Where clients report it, the view also shows
which regions machines prefer and their latency.

**Independent Test**: Add a custom region to a DERP map file. It validates and diffs, and the
live DERP map shows the region after the reload or restart the app says is needed.

---

### User Story 16 - Server Health, Backup, and Upgrades (Priority: P3) [api+host]

The server page shows version, health, API flavour, and the capability matrix, which says what
this server and mode support. It also offers maintenance actions such as backfilling machine
IPs, which needs a typed confirmation.

In host mode it adds:

- **Doctor**: checks for permissions, socket reachability, whether `server_url` is reachable,
  TLS certificate expiry, deprecated config keys, database size and WAL growth, disk space, and
  clock skew. Default thresholds, which can be configured:

  | Check | Warn | Error |
  |---|---|---|
  | Certificate expiry | under 21 days | under 7 days |
  | WAL size | over 64 MB, or over 50% of the database | — |
  | Free disk | under 10% | under 1 GB |
  | Clock skew from an NTP or HTTP Date source | over 2 s | over 30 s | Each finding has a suggested fix.
- **Backup**: a one-key snapshot bundle containing the config, policy file, extra records, DERP
  files, keys, and a consistent online copy of the database. It is stored under a configurable
  directory with retention.
- **Restore**: puts the files back. Database restore is shown as step-by-step instructions,
  never performed automatically.
- **Upgrade assistant**: compares the installed version with the latest release, explains the
  supported upgrade path (one minor version at a time, no downgrades), shows release notes,
  and takes a backup before the admin upgrades.

**Independent Test**: The doctor reports a planted expired certificate and a deprecated key.
The backup bundle contains every expected file. The capability matrix is accurate for 0.29 and
0.30 fakes in both modes.

---

### Edge Cases

- The server drops mid-session: show a banner, keep the last data visible and marked stale,
  retry with backoff, and block mutations until reconnected.
- A tailnet with thousands of machines: lists stay responsive, filtering is instant, and
  rendering is limited to what is visible.
- A tiny terminal below 80x24: show a "resize me" notice instead of broken layout. Narrow
  widths drop low-priority columns first.
- No truecolor or no Unicode: themes fall back to 256 or 16 colours and icons fall back to
  ASCII.
- The API key expires while in use: detect it and send the user to the connection editor.
- Two admins edit the policy at once: before applying, refetch and warn if the server's policy
  changed since the edit began.
- Machine names with Unicode or very long names: truncate with an ellipsis without breaking
  alignment.
- Read-only mode: mutating actions are hidden or disabled with a reason, never silently
  ignored. In host mode this covers file writes and service control too.
- Not enough permission in host mode, for example being able to read the config but not write
  it: editing is disabled with the exact reason and a fix command. The app never runs sudo by
  itself.
- headscale runs in a container: the container is detected, the CLI, logs, and restart go
  through the container runtime, and the config is found through its mounted path. If it cannot
  be found, the app asks for the path.
- A restart leaves headscale unhealthy: show the last log lines and offer to restore the backup
  and restart again.
- A file is changed by another tool while it is open in Headsup: detect it by content hash
  before writing and show a three-way diff.
- The database is Postgres instead of SQLite: database-sourced machine detail uses the
  configured connection read-only, or is turned off with an explanation.
- The server's database schema is newer than Headsup understands: database-sourced extras are
  turned off with a version notice, and everything else keeps working.

## Requirements *(mandatory)*

### Functional Requirements

**Connection and profiles**

- **FR-001**: Users MUST be able to connect with a server address and API key supplied by
  flag, environment variable, or a named profile in config.
- **FR-002**: Users MUST be able to keep several profiles and switch between them at runtime.
- **FR-003**: The system MUST support TLS and plaintext connections. Skipping certificate
  checks MUST be an explicit option that the UI flags as insecure.
- **FR-004**: API keys MAY be stored in the OS keychain instead of the config file.
- **FR-005**: The system MUST detect the server version on connect and warn when it is
  unsupported.

**Machines**

- **FR-010**: The system MUST list machines with sortable, configurable columns and live
  filtering, including field tokens.
- **FR-011**: Users MUST be able to view machine detail: all IPs, tags, available, approved,
  and served routes, expiry, last seen, created, registration method, user, pre-auth key used,
  and short machine and node key fingerprints.
- **FR-012**: Users MUST be able to rename, set tags, expire (with or without disabling
  expiry), delete, and approve or revoke routes on a machine.
- **FR-013**: Users MUST be able to multi-select machines for bulk tag, expire, and delete.
- **FR-014**: Users MUST be able to register a pending machine from a registration key or a
  full URL, and approve or reject web-auth registrations from a pending queue.
- **FR-015**: Users MUST be able to copy names, IPs, and keys to the clipboard, using OSC52 when
  remote.

**Users, keys, routes, policy**

- **FR-020**: Users MUST be able to list, create, rename, and delete users. Deleting a user who
  owns machines MUST be blocked.
- **FR-021**: Users MUST be able to list, create, expire, and delete pre-auth keys, and see the
  matching `tailscale up` command.
- **FR-022**: Users MUST be able to list, create, expire, and delete API keys. Secrets are shown
  only at creation.
- **FR-023**: The system MUST provide a tailnet-wide route and exit-node view with approve and
  revoke.
- **FR-024**: Users MUST be able to view, explore, edit (inline or `$EDITOR`), validate, diff,
  and apply the policy, and roll back to a locally kept earlier version.
- **FR-025**: The system MUST provide an access preview that shows what a chosen source can
  reach under the current or edited policy.

**Experience**

- **FR-030**: Every action MUST have a key binding and a command-palette entry. Bindings MUST
  be remappable.
- **FR-031**: Destructive actions MUST require confirmation naming the target. Bulk actions
  MUST show the count and the list.
- **FR-032**: The system MUST ship 20 or more built-in themes, load user theme files, switch
  themes live, and persist the choice.
- **FR-033**: The system MUST offer Unicode, Nerd Font, and ASCII icon sets, and compact and
  comfortable density.
- **FR-034**: The system MUST auto-refresh at a configurable interval, with a manual refresh
  key.
- **FR-035**: The system MUST provide a read-only mode that disables all mutations.
- **FR-036**: Modules MUST be individually enable-able and disable-able in config.
- **FR-037**: The system MUST keep a local activity log of mutations, with secrets redacted.
- **FR-038**: The system MUST offer non-interactive subcommands with JSON, YAML, and table
  output for every list and mutation.
- **FR-039**: The system MUST show a dashboard summary on connect.
- **FR-040**: The system MUST show server version, health, and a capability matrix by mode.
  In remote mode it MUST explain that host-only areas (DNS, DERP, server settings, service,
  logs) need host mode, instead of hiding them.
- **FR-041**: The system MUST support IP backfill where the server supports it. OAuth client
  management (0.30 and later) is deferred to v1.1. Until then, the capability matrix MUST show
  it as "planned".

**Host mode**

- **FR-050**: The system MUST find headscale's config the same way headscale does: flag, then
  environment variable, then the standard paths. It MUST then connect locally with no API key
  wherever permissions allow.
- **FR-051**: The system MUST detect the service manager (systemd, OpenRC, container runtime,
  or none), the log source, and the permissions available. Each host feature degrades on its
  own.
- **FR-052**: Every host file write MUST go through this pipeline: back up, preserve comments,
  validate (schema plus headscale's own checker where one exists), diff, confirm, write
  atomically keeping owner and mode, then offer rollback.
- **FR-053**: The system MUST detect external changes to a file before writing it and refuse to
  overwrite blindly.
- **FR-054**: The system MUST provide a structured, sectioned editor for the main config. Each
  field shows its default, a description, and whether it applies on save or needs a restart.
- **FR-055**: The system MUST provide a DNS editor covering MagicDNS, base domain, global and
  split nameservers, search domains, and extra records. Extra records go to the hot-reloaded
  file when one is configured.
- **FR-056**: The system MUST allow file-mode policies to be edited on the host, validating with
  headscale's checker and signalling a policy reload after writing.
- **FR-057**: The system MUST show service state and allow start, stop, restart, and reload. A
  restart MUST be preceded by a passing config check and followed by a health check.
- **FR-058**: The system MUST provide a live, filterable log viewer over the detected log
  source.
- **FR-059**: The system MUST read extra machine detail (OS, client version, DERP preference
  and latency, endpoints) from the database, read-only, and MUST NEVER write to the database.
- **FR-060**: The system MUST use the server's local debug and metrics data, where available,
  for pending registrations, primary routes, the compiled filter, the live DERP map, node ping,
  and dashboard metrics.
- **FR-061**: The system MUST provide a DERP editor for embedded server settings and custom map
  files.
- **FR-062**: The system MUST provide a doctor, backup and restore (files only; database restore
  is instructions only), and an upgrade assistant.
- **FR-064**: Backups and pre-write file copies contain secrets: private keys, the OIDC client
  secret, and the database. The system MUST therefore:
  - create backup directories with mode 0700 and backup files with mode 0600, owned by the
    running user;
  - refuse to write into a backup directory that is group- or world-accessible, or owned by
    another user;
  - never record secret values in manifests, the activity log, or toasts.

  Manifests store only paths, hashes, ownership, and mode.
- **FR-063**: The system MUST NEVER escalate privileges by itself. It shows the exact command
  instead.

### Key Entities

- **Profile**: a named connection: address, credential reference, TLS options, and read-only
  flag.
- **Machine (Node)**: a device in the tailnet. Has an owner, IPs, tags, routes, expiry,
  online state, and client info.
- **User**: an identity that owns machines and keys. Local or from an identity provider.
- **Pre-auth Key**: a credential that lets a device join. Has owner, reusable/ephemeral flags,
  tags, and expiry.
- **API Key**: an admin credential for the API. Identified by prefix, with an expiry.
- **Route**: a subnet prefix or exit-node advertisement from a machine. It is advertised,
  approved, and possibly primary.
- **Policy**: the access control document. Has groups, tag owners, hosts, and rules.
- **Theme**: a named 16-colour palette with light or dark polarity.
- **Module**: a pluggable area of the app with its own view, actions, and help.
- **Activity Entry**: a timestamped record of a mutation made through the app, covering file
  writes and service actions as well as API calls.
- **Host Environment**: what was found on this machine: config path, headscale version, local
  transport, service manager, log source, database kind, and permissions.
- **Managed File**: a headscale-owned file Headsup can edit (main config, policy, extra records,
  DERP map). It has a path, owner and mode, a content hash, a validator, and how changes take
  effect: live, on reload, or on restart.
- **Backup**: a timestamped bundle of managed files plus a database copy, with a manifest.
- **Log Entry**: time, level, message, and structured fields, linked to a machine where one is
  mentioned.
- **DNS Record**: name, type, and value.
- **DERP Region**: ID, code, name, and nodes (host, ports, IPs).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new user goes from download to seeing their tailnet in under 2 minutes with only
  a server address and API key.
- **SC-002**: Every operation the headscale API exposes is reachable in the TUI: 100% parity,
  checked against a published capability matrix.
- **SC-003**: Common tasks take three keystrokes or fewer after the target is selected: rename,
  expire, approve route, and create pre-auth key.
- **SC-004**: With 2,000 machines, filtering updates visibly within the next frame, and screen
  switches feel instant (under 100 ms perceived).
- **SC-005**: No destructive action can complete without an explicit confirmation step. This is
  verified by tests for every mutating action.
- **SC-006**: Every screen is usable at 80x24 and in every bundled theme. This is verified by
  golden snapshots.
- **SC-007**: A contributor can add a new module by touching only that module's directory plus
  one registration line.
- **SC-008**: The community adopts it: the project reaches 100+ GitHub stars and contributors
  submit themes within 3 months of release.
- **SC-009**: On a stock headscale package install, running `headsup` as root or as a member of
  the `headscale` group reaches the dashboard with zero flags and zero config in under 3
  seconds.
- **SC-010**: Every host file change can be rolled back with one action. In tests, 100% of
  failed validations leave the file byte-identical.
- **SC-011**: An admin can change a DNS setting, a config setting, or a policy file on the host
  without opening a text editor or reading headscale docs.

## Assumptions

- Target headscale versions are 0.29.x (the current stable release) and 0.30 (the upcoming
  release). Both expose the same REST endpoints, so one client serves both. Older versions may
  get degraded support.
- Some console features have no headscale equivalent: device posture, network flow logs,
  Funnel, billing, admin audit logs, webhooks, and changing a machine's owner. These are out of scope.
  They are listed in a capability matrix as "not supported by headscale".
- DNS, DERP, OIDC, and server settings are config-file-only in headscale. Headsup edits them in
  host mode only. Remote editing of server files is out of scope, because it would need an
  agent on the server.
- Policy in database mode is edited through the API in both modes. Policy in file mode is
  edited on the host.
- The primary host target is a Linux headscale package install under systemd, with
  container-based installs second.
- Headsup runs with the rights of the user who starts it. It is not a daemon and it installs
  nothing on the server.
- OAuth client management is out of scope for v1.0 and planned for v1.1. It is reached through
  headscale 0.30's `/api/v2` keys endpoint.
- In remote mode, admins have an API key, created with `headscale apikeys create`. The TUI does not do the
  OIDC login flow for admins in v1.
- The working name "Headsup" is provisional and can be renamed before release.
