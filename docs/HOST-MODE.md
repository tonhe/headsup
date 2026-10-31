# Host mode

Run headsup on the machine headscale runs on and it switches to host mode on
its own. No URL, no API key, no flags. On top of everything the API offers,
host mode edits headscale's config, DNS, DERP and policy files, controls the
service, reads the logs and the debug listener, and enriches machines with
OS, client version, DERP and endpoint detail from a read-only look at the
database.

This page covers how host mode finds headscale, what it needs permission to
do, how it behaves in containers, when a change you make takes effect, and how
backups and restores work.

## How it finds headscale

At startup headsup looks for:

1. **The config file.** `--hs-config PATH`, else `HEADSCALE_CONFIG`, else
   `[host] headscale_config` in headsup's config, else the first of
   `/etc/headscale/config.yaml`, `/etc/headscale/config.yml`,
   `~/.headscale/config.yaml` (or `.yml`) and `config.yaml` in the current
   directory. From it headsup reads the socket path, `server_url`,
   `metrics_listen_addr`, the database settings, the policy mode and path, and
   the extra-records path.
2. **A way to reach the API.** On 0.30, if the unix socket speaks HTTP,
   headsup uses it directly with no auth header (headscale trusts local
   socket clients). On 0.29, whose socket speaks gRPC, it runs the `headscale`
   CLI with `-o json` instead. In a container it runs that CLI through
   `docker exec` or `podman exec`.
3. **A service manager.** systemd, OpenRC, a container, or a bare headscale
   process (which can only be sent SIGHUP).
4. **A log source.** journald, the container's log, or a log file.

If any piece is missing, only the features that need it turn off, and the
Server page (`2 S`) and `headsup host info` say why.

Flags and config that change this:

| Setting | Effect |
|---|---|
| `--host` | Require host mode. Fail instead of falling back to a remote profile. |
| `--remote` | Never use host mode. |
| `[host] enabled = "auto"` | The default: try host mode first, then the configured profile. `"always"` and `"never"` match `--host` and `--remote`. |
| `--hs-config PATH` | Use this headscale config. |
| `[host] container = "NAME"` | Use this container, even when headscale is also installed on the host. |

Naming a remote connection also skips host mode, unless you add `--host`:
`--url`, `--profile`, `--api-key-file`, or the `HEADSUP_PROFILE`, URL or API
key environment variables.

## Permissions

headsup never escalates privileges behind your back. Each host feature is
probed separately at startup, and anything you lack is disabled with the
reason and the exact command that would fix it. `headsup host perms` prints
the whole table:

```text
PERMISSION         OK   REASON  FIX
local API          yes  -       -
read config        yes  -       -
write config       yes  -       -
write policy       yes  -       -
write DNS records  yes  -       -
control service    yes  -       -
read logs          yes  -       -
debug/metrics      yes  -       -
read database      yes  -       -
```

What each one needs:

| Permission | Used for | What grants it |
|---|---|---|
| local API | everything (machines, users, keys, routes, policy) | Access to the socket and its directory. Usually the group that owns them, often `headscale`. |
| read config | Server Settings, DNS and DERP views, doctor, backups | Read access to `config.yaml`. |
| write config | saving Server Settings, DNS and DERP changes, restores | Write access to `config.yaml` and its directory. |
| write policy | applying the policy with `policy.mode: file` | Write access to the policy file. |
| write DNS records | editing the extra-records file | Write access to that file. |
| control service | start, stop, restart, reload | Root, or a polkit rule for `headscale.service`. |
| read logs | Service and logs page | Root or the `systemd-journal`, `adm` or `wheel` group for journald, read access for a log file, nothing extra for a container. |
| debug/metrics | live map, ping, compiled filter, route primaries, metrics | headscale running, with `metrics_listen_addr` on a local address. |
| read database | OS, client, DERP and endpoint detail | Read access to the SQLite file and its directory. Postgres isn't probed. |

When a missing permission comes from a group, the fix is a group change, for
example:

```bash
sudo usermod -aG headscale $USER && newgrp headscale
```

When the file's group can't help (the file is root-only, say), the fix is to
run as root, `sudo headsup`.

### Root or the headscale group

Membership of the group that owns headscale's socket, config and database
directory (often `headscale`) covers the everyday work: the whole API, the
config views, the doctor, backups and database enrichment. Whether it also
covers writing `config.yaml`, the policy file or the records file depends on
those files' modes on your system, so check `headsup host perms`. Service
control needs root (or polkit), and reading journald needs one of the groups
in the table.

Running as root covers everything. The trade-off is that every change is made
as root. headsup keeps each file's owner, group and mode on write, so files
don't end up root-owned. Backups go to the state directory of the user
headsup runs as, which under sudo is usually root's.

### Ask to restart under sudo

When headscale is installed on the machine but the current user can't reach
it, headsup asks before giving up:

```text
headsup: host mode is not available: no access to /var/run/headscale (where the socket lives)

headscale is installed here, but this user can't reach it.
Restart headsup with sudo? [Y/n]
```

`y` (or enter) re-runs the same command line under `sudo`, which asks for
your password as usual. Anything else exits with the fix command instead. The
prompt only appears when stdin and stderr are a terminal, you aren't already
root, `sudo` is installed and the failure was missing access (not, say, a
stopped server). This prompt is the only code in headsup allowed to run
`sudo`, `su`, `doas` or `pkexec`, and a lint test enforces that. Everywhere
else, an action that needs more rights shows the command to run and leaves it
to you.

## Containers

If headscale runs in Docker or Podman, run headsup on the container host.
It doesn't need to be inside the container. headsup looks through `docker ps` and then
`podman ps` for a container whose name, image or labels mention headscale.
It prefers a headscale install on the host itself when there is one, unless
you name the container with `[host] container`.

In container mode:

- API calls run `headscale` inside the container with `docker exec` (or
  `podman exec`).
- Service control uses `docker start|stop|restart`, and reload sends SIGHUP
  with `docker kill --signal HUP`.
- Logs come from the container's log.
- Files are edited on the host side of the container's bind mounts. headsup
  reads the container's mounts and maps paths such as
  `/etc/headscale/config.yaml` to their host location. A file that isn't
  bind-mounted can't be edited, and the Server page says so.
- Live debug data needs `metrics_listen_addr` published on a local port.
  `docker port NAME` shows what is.

Access to the Docker socket is equivalent to root on that machine, so a user
in the `docker` group gets full host mode for the container without sudo.

## Managed files and when changes take effect

Every file headsup edits goes through the same pipeline, in
`internal/host/files`:

1. Read the file and record its SHA-256.
2. Build the change, preserving comments and formatting.
3. Validate on a temporary copy written next to the target, never on the live
   file.
4. Show a diff and ask. Riskier changes need the target's name typed.
5. Re-read the file. If it changed on disk since step 1, stop and offer a
   three-way merge.
6. Back up the current content (see below).
7. Write a temporary file, fsync it, restore the original owner, group and
   mode, rename it over the target and fsync the directory.
8. Offer a one-key rollback (`u`).

An invalid change is refused, and the live file stays byte-identical.

| File | Edited from | Validated with | Takes effect |
|---|---|---|---|
| `config.yaml` | Server Settings (`2 s`), DNS (`2 d`), DERP (`2 D`) | the typed field catalog, then `headscale configtest` | after a restart |
| Policy file (`policy.mode: file`) | Access controls (`1 a`) | HuJSON parse, then `headscale policy check` | on reload (SIGHUP) |
| Extra DNS records (`dns.extra_records_path`) | DNS (`2 d`) | JSON or YAML schema (name, type A or AAAA, value) | live: headscale watches the file |
| DERP map files (`derp.paths`) | DERP (`2 D`) | schema (regions, nodes, ports) | after a restart |

Details per file:

- **`config.yaml`.** Every key needs a restart. Server Settings marks
  unsaved and restart-needed fields, and after a save it offers to restart.
  A restart always runs `headscale configtest` first and refuses if it fails,
  and it warns that connected clients will reconnect. If headscale comes back
  unhealthy, headsup shows the last log lines and offers to restore the
  previous file and restart again. Changes to `server_url`, `listen_addr`,
  `prefixes.*`, `database.*` and `noise.*` need the name typed to confirm.
  `m` migrates deprecated keys, such as 0.29's flat TLS keys to 0.30's
  `tls.*`.
- **Policy file.** Applying writes the file, reloads headscale (`systemctl
  reload`, `rc-service reload`, or SIGHUP), and checks that the new policy is
  active. If it isn't, headsup offers a rollback. With `policy.mode:
  database` the policy goes through the API instead, in both modes.
- **Extra records file.** headscale reloads it by itself, so record edits are
  live and need no restart. Records written inline under
  `dns.extra_records` live in `config.yaml` and need a restart. The DNS page
  can move inline records into a file in one confirmed action. The file keeps
  its format (JSON, HuJSON or YAML) and its comments.
- **DERP map files.** Treated as needing a restart. headsup assumes headscale
  doesn't pick up map file changes on its own. Files keep their format (YAML
  with headscale's lower-case keys, or Tailscale's JSON or HuJSON).

Everything else in DNS and DERP (MagicDNS, base domain, nameservers, split
DNS, search domains, the embedded DERP server, map URLs) is `config.yaml` and
needs a restart. The DNS and DERP pages mark each item live or
"needs restart".

`$EDITOR` (`E`) works on every file and goes through the same validation,
diff and confirmation as the forms.

## Backups

There are two kinds: a backup taken automatically before every write, and
bundles you take on demand.

### Per-write backups

Before every write, the pipeline copies the current file to

```text
$XDG_STATE_HOME/headsup/backups/<timestamp>/<path of the file>
```

(`~/.local/state/headsup/backups/` when `XDG_STATE_HOME` isn't set) with a
`manifest.json` that records the path, SHA-256, owner, group and mode. The
last 50 backups of each file are kept. Set `[host] backup_dir` to put them
somewhere else and `[host] backup_keep` to keep more or fewer.

Rollback (`u` on the page that made the change) restores the backup, but only
if the file hasn't changed again since, and it backs up the current content
first. The Activity page (`3 v`) shows each change with its backup ID.

### Bundles

`b` on the Server page, or `headsup backup create`, takes a snapshot of the
whole server into `bundles/<id>/` under the backup directory
(`[host] backup_dir` if set, else the default above):

- `config.yaml`, the policy file, the extra-records file and DERP map files.
- The noise private key and the embedded DERP server's private key.
- A consistent copy of the SQLite database, made with SQLite's online backup
  API while headscale keeps running. For Postgres, headsup prints the
  `pg_dump` command to run yourself.

The last 10 bundles are kept. `B` on the Server page, or
`headsup backup ls`, lists them.

### Security of backup directories

Bundles hold private keys, and per-write backups can hold secrets from
`config.yaml`. So:

- Directories are created with mode 0700 and files with mode 0600.
- headsup refuses to write when the backup root is a symlink, is group- or
  world-accessible, or belongs to another user, and shows the command that
  fixes it.
- Manifests hold paths, hashes, ownership and mode only. Never content.

### Restore

`r` on a selected bundle, or `headsup backup restore ID`, restores files
only:

- The bundle is checked against its manifest first and refused if anything
  in it was altered.
- Config, policy, records and DERP files go back through the managed-file
  pipeline: validated, the current file backed up, written atomically with
  its owner and mode. Files that already match are left alone.
- Private keys are written atomically with their recorded mode and, where the
  process is allowed to set it, owner.
- The database is never written. headsup prints the steps instead: stop
  headscale, move the live database (and its `-wal` and `-shm` files) aside,
  copy the backup in, start headscale. For Postgres it prints the
  `pg_restore` command.

A restore changes files that need a restart to take effect, so restart
headscale afterwards (`X` on the Service page, or your service manager).
