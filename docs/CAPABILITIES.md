# Capabilities

What headsup can do against which headscale version, in which mode, and how
that lines up with the Tailscale admin console.

The tables between `BEGIN GENERATED` and `END GENERATED` markers are built
from the code by `internal/capsdoc` (run `make docs`). A test fails when they
drift from the code, so don't edit them by hand. The prose around them is
hand-written.

Supported headscale versions: 0.29.x and 0.30. Other versions still connect,
with a warning toast at startup.

## Two modes

**Remote mode** talks to headscale's REST API (`/api/v1`) with an API key,
from anywhere that can reach the server.

**Host mode** runs on the headscale server itself. It reaches the API over
the local unix socket (0.30) or by running the `headscale` CLI (0.29, and
containers through `docker exec` or `podman exec`), so it needs no API key.
It also reads and writes headscale's files, controls the service, and reads
the logs, the debug listener and the database (read-only).
[HOST-MODE.md](HOST-MODE.md) covers permissions and safety.

The Server page (`2 S`) shows the same matrix live for the server you're
connected to, with a reason for every feature you can't use right now.
`headsup server caps` prints the capability flags.

## Capability flags by headscale version

Some API features depend on the server version. headsup derives the flags
from the version at connect time and gates actions on them. An action whose
flag is missing is disabled with the reason in the TUI and the palette, and
the CLI exits with code 5.

<!-- BEGIN GENERATED: versions -->
| Capability | 0.29.0 | 0.29.6 | 0.30.0 | What it allows |
|---|---|---|---|---|
| health endpoint | yes | yes | yes | GET /health answers when headscale and its database are up. |
| policy check | yes | yes | yes | Validate a policy on the server before saving it. |
| policy writable via API | yes | yes | yes | The API can change the policy only when policy.mode is database; with file mode, edit it on the host. |
| web-auth approval | yes | yes | yes | Approve or reject web logins waiting in Add device. |
| pre-auth key delete | yes | yes | yes | Delete pre-auth keys, not just expire them. |
| API key delete | yes | yes | yes | Delete API keys, not just expire them. |
| OAuth clients (planned) | no | no | no | Planned in headscale; not available yet. |
| problem+json errors | no | no | yes | Structured RFC 7807 error responses. |
| tag-only pre-auth keys | no | no | yes | Pre-auth keys owned by tags instead of a user. |
| backfill node IPs | yes | yes | yes | Give machines missing an address one from the configured prefixes (F). |
| disable node expiry | no | yes | yes | Mark a machine's key as never expiring. |
<!-- END GENERATED: versions -->

`policy writable via API` is also learned at runtime: when the server refuses
a policy write because `policy.mode` is `file`, headsup turns the flag off.

## Features by mode

API features work in both modes when the server supports them. Every row
after the API flags exists only in headscale's files, service or database, so
it needs host mode.

<!-- BEGIN GENERATED: modes -->
| Feature | Remote mode | Host mode | Notes |
|---|---|---|---|
| Health endpoint | yes | yes | GET /health answers when headscale and its database are up. |
| Policy check | yes | yes | Validate a policy on the server before saving it. |
| Policy writable via API | yes | yes | The API can change the policy only when policy.mode is database; with file mode, edit it on the host. |
| Web-auth approval | yes | yes | Approve or reject web logins waiting in Add device. |
| Pre-auth key delete | yes | yes | Delete pre-auth keys, not just expire them. |
| API key delete | yes | yes | Delete API keys, not just expire them. |
| OAuth clients (planned) | no | no | Planned in headscale; not available yet. |
| Problem+json errors | yes | yes | Structured RFC 7807 error responses. |
| Tag-only pre-auth keys | yes | yes | Pre-auth keys owned by tags instead of a user. |
| Backfill node IPs | yes | yes | Give machines missing an address one from the configured prefixes (F). |
| Disable node expiry | yes | yes | Mark a machine's key as never expiring. |
| Server Settings (config.yaml) | no | yes | Config file only: headscale has no API for its settings. Edit them on the host. Docs: https://headscale.net/stable/ref/configuration/ |
| DNS: MagicDNS, nameservers, records | no | yes | Config file only (dns.*, and the extra-records file). Docs: https://headscale.net/stable/ref/dns/ |
| DERP map and embedded relay | no | yes | Config file only (derp.*, plus local map files). Docs: https://headscale.net/stable/ref/derp/ |
| OIDC login | no | yes | Config file only (oidc.*). Docs: https://headscale.net/stable/ref/oidc/ |
| TLS and ACME certificates | no | yes | Config file only (tls.*). Docs: https://headscale.net/stable/ref/tls/ |
| Policy file (policy.mode: file) | no | yes | With policy.mode file the policy lives on disk and the API cannot change it. Docs: https://headscale.net/stable/ref/acls/ |
| Service start, stop, restart | no | yes | Uses the service manager (systemd, OpenRC, container) on the host. |
| Server logs | no | yes | Reads journald, the container log or the log file on the host. |
| Live debug data and metrics | no | yes | Reads metrics_listen_addr (/debug, /metrics), which is bound to localhost by default. |
| Machine OS, client, DERP detail | no | yes | Read-only from the database; the API does not expose host info. |
| Doctor | no | yes | Checks permissions, socket, server_url, certificates, deprecated keys, database, disk and clock. |
| Backup and restore (files) | no | yes | Bundles the config, policy, records, DERP files, keys and an online database copy. Database restore is given as steps. |
| Upgrade assistant | partly | yes | Compares with the latest release and prints the upgrade commands; it never runs them. |
<!-- END GENERATED: modes -->

## Endpoint table

Every `api.Backend` method, the request the REST client sends, the command
the CLI transport runs, the capability that gates it, and the pages and
actions that use it. `{id}`, `{name}` and the other braces are placeholders.
The requests and commands are recorded from the real `httpapi` and `execcli`
backends, and the gates are probed on the fake backend.

<!-- BEGIN GENERATED: endpoints -->
| Backend method | REST `/api/v1` | headscale CLI (host mode) | Gate | Used by | Notes |
|---|---|---|---|---|---|
| Connect | `GET /version`, then `GET /health` | `headscale version`, then `headscale health` | none | `app` | Version and health at startup; capabilities are derived from the version. |
| Capabilities | none | none | none | `app` | Local: no request. |
| ListNodes | `GET /node?user={user}` | `headscale nodes list -u {user}` | none | `dashboard`, `machines`, `routes` |  |
| GetNode | `GET /node/{id}` | `headscale nodes list` | none | `machines.detail` |  |
| RegisterNode | `POST /node/register?key={key}&user={user}` | `headscale nodes register -u {user} -k {key}` | none | `pending.paste` |  |
| RenameNode | `POST /node/{id}/rename/{name}` | `headscale nodes rename -i {id} {name}` | none | `machines.rename` |  |
| SetTags | `POST /node/{id}/tags` | `headscale nodes tag -i {id} --tags=tag:demo` | none | `machines.tags` |  |
| SetApprovedRoutes | `POST /node/{id}/approve_routes` | `headscale nodes approve-routes -i {id} --routes=10.0.0.0/24` | none | `routes.approve`, `routes.revoke`, `routes.approveall`, `machines.routes` |  |
| ExpireNode | `POST /node/{id}/expire` | `headscale nodes expire -i {id}` | disable node expiry | `machines.expire`, `machines.noexpire` | The gate applies to disabling expiry only (`disableExpiry`, CLI `-d`). |
| DeleteNode | `DELETE /node/{id}` | `headscale nodes delete -i {id} --force` | none | `machines.delete` |  |
| BackfillIPs | `POST /node/backfillips?confirmed=true` | `headscale nodes backfillips --force` | backfill node IPs | `server.backfill` |  |
| ListUsers | `GET /user` | `headscale users list` | none | `users`, `dashboard` |  |
| CreateUser | `POST /user` | `headscale users create {name}` | none | `users.create` |  |
| RenameUser | `POST /user/{id}/rename/{name}` | `headscale users rename -i {id} -r {name}` | none | `users.rename` |  |
| DeleteUser | `DELETE /user/{id}` | `headscale users destroy -i {id} --force` | none | `users.delete` |  |
| ListPreAuthKeys | `GET /preauthkey` | `headscale preauthkeys list` | none | `keys`, `pending` |  |
| CreatePreAuthKey | `GET /user?name={user}`, then `POST /preauthkey` | `headscale users list -n {user}`, then `headscale preauthkeys create -u {id} --reusable` | tag-only pre-auth keys | `keys.new`, `pending.newkey` | The user name is resolved to its ID first. The gate applies to tag-only keys (no user). |
| ExpirePreAuthKey | `POST /preauthkey/expire` | `headscale preauthkeys expire -i {id}` | none | `keys.expire` |  |
| DeletePreAuthKey | `DELETE /preauthkey?id={id}` | `headscale preauthkeys delete -i {id} --force` | pre-auth key delete | `keys.delete` |  |
| ListAPIKeys | `GET /apikey` | `headscale apikeys list` | none | `keys` |  |
| CreateAPIKey | `POST /apikey` | `headscale apikeys create -e {duration}` | none | `keys.new` |  |
| ExpireAPIKey | `POST /apikey/expire` | `headscale apikeys expire -p {prefix}` | none | `keys.expire` |  |
| DeleteAPIKey | `DELETE /apikey/{prefix}` | `headscale apikeys delete -p {prefix} --force` | API key delete | `keys.delete` |  |
| GetPolicy | `GET /policy` | `headscale policy get` | none | `policy` |  |
| CheckPolicy | `POST /policy/check` | `headscale policy check -f {file}` | policy check | `policy.check` |  |
| SetPolicy | `PUT /policy` | `headscale policy set -f {file}` | policy writable via API | `policy.apply`, `policy.rollback` | Only with `policy.mode: database`. In file mode, host mode writes the policy file instead. |
| AuthRegister | `POST /auth/register` | `headscale auth register --auth-id {auth_id} -u {user}` | web-auth approval | `pending.approve` |  |
| AuthApprove | `POST /auth/approve` | `headscale auth approve --auth-id {auth_id}` | web-auth approval | `pending.approve` |  |
| AuthReject | `POST /auth/reject` | `headscale auth reject --auth-id {auth_id}` | web-auth approval | `pending.reject` |  |
<!-- END GENERATED: endpoints -->

## Actions by page

Every module action, grouped by page in sidebar order. These are the same
actions behind the keys, the `:` palette, the help screen and the CLI verbs.
"Confirm" is the confirmation the app enforces before it runs the action:
`y/N`, or typing the target's name. Pages that stage edits (Access controls,
Server Settings, DNS, DERP) also show a diff and ask before they write, so
their apply actions can show an empty Confirm column and still never write
without asking. "Needs" is the server capability or host permission the
action requires.

<!-- BEGIN GENERATED: actions -->
### Dashboard (`dashboard`)

Tailnet section, remote and host (more on the host).

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `dashboard.open` | Go to issue | `enter` |  |  |

### Machines (`machines`)

Tailnet section, remote and host (more on the host).

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `machines.detail` | Toggle detail pane | `enter` |  |  |
| `machines.rename` | Rename machine | `r` |  |  |
| `machines.tags` | Edit tags | `t` |  |  |
| `machines.routes` | Approve routes | `a` | y/N |  |
| `machines.expire` | Expire machine | `e` | y/N |  |
| `machines.noexpire` | Disable key expiry | `E` | y/N | disable node expiry |
| `machines.delete` | Delete machine | `D` | type the name |  |
| `machines.owner` | Change owner (not supported by headscale) | `O` |  |  |
| `machines.copyip` | Copy IP address | `y` |  |  |
| `machines.copyname` | Copy name | `Y` |  |  |
| `machines.ping` | Ping from server | `p` |  | host: debug/metrics |
| `machines.sort` | Sort by next column | `s` |  |  |
| `machines.sortrev` | Reverse sort | `S` |  |  |

### Users (`users`)

Tailnet section, remote and host.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `users.create` | New user | `n` |  |  |
| `users.rename` | Rename user | `r` |  |  |
| `users.delete` | Delete user | `D` | type the name |  |
| `users.machines` | Show this user's machines | `enter` |  |  |
| `users.keys` | Show this user's keys | `K` |  |  |
| `users.copyname` | Copy user name | `y` |  |  |

### Routes (`routes`)

Tailnet section, remote and host (more on the host).

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `routes.approve` | Approve route | `a` | y/N |  |
| `routes.revoke` | Revoke route | `x` | y/N |  |
| `routes.approveall` | Approve all routes for this machine | `A` | y/N |  |
| `routes.machine` | Open the machine | `enter` |  |  |

### Add device (`pending`)

Tailnet section, remote and host (more on the host).

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `pending.newkey` | New pre-auth key | `n` |  |  |
| `pending.paste` | Register by key or URL | `p` |  |  |
| `pending.approve` | Approve device | `a` |  | web-auth approval |
| `pending.reject` | Reject device | `x` | y/N | web-auth approval |
| `pending.queue` | Add a web-login request to the queue | `P` |  | web-auth approval |
| `pending.copycmd` | Copy tailscale up command | `c` |  |  |
| `pending.copykey` | Copy key | `y` |  |  |
| `pending.qr` | Show QR code | `Q` |  |  |

### Keys (`keys`)

Tailnet section, remote and host.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `keys.new` | New pre-auth key | `n` |  |  |
| `keys.expire` | Expire key | `e` | y/N |  |
| `keys.delete` | Delete key | `D` | type the name | pre-auth key delete |
| `keys.copycmd` | Copy tailscale up command | `c` |  |  |
| `keys.copykey` | Copy new key | `y` |  |  |
| `keys.qr` | Show QR code | `Q` |  |  |

### Access controls (`policy`)

Tailnet section, remote and host (more on the host).

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `policy.edit` | Edit policy | `e` |  |  |
| `policy.editor` | Edit policy in $EDITOR | `E` |  |  |
| `policy.check` | Check policy | `v` |  |  |
| `policy.diff` | Diff unsaved edits | `d` |  |  |
| `policy.apply` | Check and apply policy | `ctrl+s` |  |  |
| `policy.discard` | Discard unsaved edits | `D` | y/N |  |
| `policy.history` | Policy history | `h` |  |  |
| `policy.explore` | Explore policy | `x` |  |  |
| `policy.preview` | Access preview (who can reach what) | `p` |  |  |
| `policy.filter` | Compiled packet filter | `f` |  | host: debug/metrics |
| `policy.rollback` | Roll back policy | `u` |  |  |

### Server (`server`)

Server section, remote and host (more on the host).

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `server.doctor` | Run the doctor | `c` |  |  |
| `server.fix` | Go to the fix for this finding | `f` |  |  |
| `server.copyfix` | Copy the fix command | `y` |  |  |
| `server.backup` | Back up now | `b` |  | host: read config |
| `server.backups` | Show backups | `B` |  |  |
| `server.restore` | Restore files from the selected backup | `r` | type the name | host: write config |
| `server.upgrade` | Check for upgrades | `U` |  |  |
| `server.backfill` | Backfill machine IPs | `F` | type the name | backfill node IPs |
| `server.openconfig` | Open the local config in Server Settings | `o` |  |  |

### Server Settings (`serverconfig`)

Server section, host only.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `serverconfig.edit` | Edit field | `enter` |  | host: read config |
| `serverconfig.reset` | Reset field to default | `x` |  | host: read config |
| `serverconfig.editor` | Edit in $EDITOR | `E` |  | host: read config |
| `serverconfig.migrate` | Migrate deprecated keys | `m` |  | host: read config |
| `serverconfig.diff` | Show unsaved changes | `d` |  |  |
| `serverconfig.apply` | Review and save | `ctrl+s` |  | host: read config |
| `serverconfig.restart` | Restart headscale | `X` | y/N | host: control service |
| `serverconfig.rollback` | Restore the backup | `u` | y/N | host: write config |
| `serverconfig.discard` | Discard unsaved changes | `U` | y/N |  |

### DNS (`dns`)

Server section, host only.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `dns.edit` | Edit | `enter` `e` |  | host: write config |
| `dns.add` | Add record / split domain | `n` |  | host: write config |
| `dns.delete` | Delete record / remove split domain | `D` |  | host: write config |
| `dns.move` | Move inline records to a file | `M` |  | host: write config, write DNS records |
| `dns.diff` | Show diff | `d` |  |  |
| `dns.apply` | Apply DNS changes | `ctrl+s` | y/N |  |
| `dns.discard` | Discard staged changes | `x` | y/N |  |
| `dns.rollback` | Roll back the last DNS change | `u` | y/N |  |

### DERP (`derp`)

Server section, host only.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `derp.edit` | Edit | `enter` `e` |  | host: write config |
| `derp.add` | Add source / region / node | `n` |  | host: write config |
| `derp.delete` | Remove source / region / node | `D` |  | host: write config |
| `derp.newfile` | New DERP map file | `N` |  | host: write config |
| `derp.refresh` | Refresh the live map | `r` |  |  |
| `derp.diff` | Show diff | `d` |  |  |
| `derp.apply` | Apply DERP changes | `ctrl+s` | y/N | host: write config |
| `derp.discard` | Discard staged changes | `x` | y/N |  |
| `derp.rollback` | Roll back the last DERP change | `u` | y/N |  |

### Service (`service`)

Server section, host only.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `service.start` | Start headscale | `S` | y/N | host: control service |
| `service.stop` | Stop headscale | `x` | type the name | host: control service |
| `service.restart` | Restart headscale | `X` | y/N | host: control service |
| `service.reload` | Reload policy | `H` | y/N | host: control service |
| `service.follow` | Follow newest log lines | `f` `G` |  |  |
| `service.level` | Cycle log level filter | `l` |  |  |
| `service.search` | Search logs (regex) | `/` |  |  |
| `service.pause` | Pause or resume logs | `space` |  |  |
| `service.nexterr` | Jump to next error | `n` |  |  |
| `service.preverr` | Jump to previous error | `N` |  |  |
| `service.expand` | Show line fields | `enter` |  |  |
| `service.machine` | Open mentioned machine | `m` |  |  |

### Activity (`activity`)

App section, local.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `activity.filter` | Filter activity | `/` |  |  |
| `activity.export` | Export activity to JSON | `x` |  |  |

### Preferences (`prefs`)

App section, local.

| Action | Title | Keys | Confirm | Needs |
|---|---|---|---|---|
| `prefs.edit` | Change setting | `enter` |  |  |
| `prefs.next` | Next value | `l` `right` `space` |  |  |
| `prefs.prev` | Previous value | `h` `left` |  |  |
| `prefs.reset` | Reset setting to default | `x` |  |  |
<!-- END GENERATED: actions -->

## Parity with the Tailscale admin console

headscale implements the Tailscale control plane, so most of the console's
day-to-day pages have an equivalent here.

| Console page | headsup | Mode |
|---|---|---|
| Machines | Machines (`1 m`): filter, sort, detail, rename, tags, expire, disable expiry, delete, ping (host) | remote and host |
| Machine routes and exit nodes | Routes (`1 r`): subnet routes grouped into HA sets, exit nodes, approve and revoke | remote and host |
| Add device | Add device (`1 p`): register by key or URL, approve or reject web logins, new pre-auth key with QR | remote and host |
| Users | Users (`1 u`): create, rename, delete | remote and host |
| Access controls | Access controls (`1 a`): edit, check, diff, apply, history, rollback, explorer, access preview | remote and host with `policy.mode: database`, host only with `policy.mode: file` |
| DNS | DNS (`2 d`): MagicDNS, nameservers, split DNS, search domains, records | host only |
| Settings: Keys | Keys (`1 k`): pre-auth and API keys | remote and host |
| Settings: OAuth clients | Not yet. The matrix lists them as planned. | none |
| Logs: configuration audit | Activity (`3 v`) records the changes this app made, kept locally on the machine that made them. | local |

The server has pages the console never needed, because Tailscale runs that
part for you: Server Settings (`2 s`, headscale's `config.yaml`), DERP
(`2 D`), Service and logs (`2 l`), and the Server page (`2 S`) with the
doctor, backups and the upgrade assistant. All but the Server page are host
only, and the Server page's doctor and backups need host mode too.

## Headscale gaps

These console features have no headscale equivalent, so headsup can't offer
them in either mode:

- Changing a machine's owner. Pressing `O` on the Machines page says so and
  suggests re-registering the machine under the new user.
- Device posture.
- Network flow logs.
- Funnel.
- Admin audit logs kept by the server. Activity only covers this app's own
  changes.
- Webhooks.
- Billing.

Other settings exist in headscale only as config, never through the API: DNS,
DERP, OIDC, TLS and every other `config.yaml` key, plus the policy when
`policy.mode` is `file`. A client that only talks to the API can't change
them. That includes headsup in remote mode and any web UI that doesn't run on
the server with access to its files.
