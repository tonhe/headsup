# Quickstart and Validation Guide

## Prerequisites

- Go 1.25 or later and `make`.
- Optional: Docker, for a real headscale server.
- Optional: `vhs`, for demo recording.

## 1. Zero-setup demo (fake backend)

```bash
make build
./bin/headsup --demo
```

Expected: the dashboard shows 51 machines (42 online), 4 users, 2 pending routes and 3 keys
expiring soon. `./bin/headsup --demo --host` adds the host pages and 2 devices waiting to
register. Pages are grouped into sections: press a section digit (`1` Tailnet, `2` Server, `3` App), then the page key shown in the sidebar. `g <letter>` chords and the `:` palette also work.

## 2. Against real headscale

```bash
docker run -d --name hs -p 8080:8080 -v $PWD/testdata/headscale:/etc/headscale \
  headscale/headscale:0.29.6 serve
docker exec hs headscale users create alice
export HEADSCALE_URL=http://localhost:8080
export HEADSCALE_API_KEY=$(docker exec hs headscale apikeys create -o json | jq -r .)
./bin/headsup
```

Validation scenarios. Each one maps to an acceptance scenario in the spec.

| # | Do | Expect |
|---|---|---|
| V1 | Launch with the wrong key | "Unauthorized" message with a hint, and the connection editor opens (US1) |
| V2 | `1 k`, `n`: create a reusable pre-auth key for alice with tag `tag:server` | The key is shown once, the `tailscale up` command can be copied, and `Q` shows a QR code (US3, US7) |
| V3 | Join a client with that key, then press `1 m` | The machine appears within 5s with the tag and is online (US2) |
| V4 | On the machine: `r`, rename it to `nas-01` | The table updates, and `3 v` shows the activity entry (US2, US12) |
| V5 | Client advertises `10.0.0.0/24`, then `1 r` | The route shows as pending with a badge. `a` approves it after a confirm (US6) |
| V6 | `1 a`, `E`, add a broken rule, save | Validation error with line and column. The buffer is kept (US5) |
| V7 | Fix the rule and save | The diff shows only the new rule. `ctrl+s` applies it after a confirm (US5) |
| V8 | `T`, then scroll through themes | Every view repaints live. The choice persists across restarts (US8) |
| V9 | `:` then type "expire" | The palette lists every expire action (US9) |
| V10 | `headsup nodes ls -o json \| jq length` | Matches the TUI's machine count (US11) |
| V11 | `--read-only`, then `D` on a machine | Blocked with a "read-only profile" toast (FR-035) |
| V12 | Resize to 80x24 | The sidebar becomes tabs and low-priority columns drop. Nothing wraps (SC-006) |

## 3. Host mode (on the headscale server)

```bash
sudo usermod -aG headscale $USER   # socket access (or run as root)
newgrp headscale
headsup                              # zero flags
```

| # | Do | Expect |
|---|---|---|
| H1 | Launch with no flags | The header shows `host:<hostname>` and the dashboard has live metric sparklines (US1) |
| H2 | Launch as a user outside the `headscale` group | A permission explainer naming what is missing, with the `usermod` fix command and a remote-mode option. On a terminal it also offers to restart under sudo (US1) |
| H3 | `1 m`, open a machine | OS, client version, DERP region and latency, and endpoints appear. `p` pings (US2) |
| H4 | Start `tailscale up` on a new client (web login) | Add device (`1 p`) shows "headscale reports 1 device waiting"; pasting its URL with `p` registers it (US3) |
| H5 | `2 d`, add record `nas.home.arpa A 100.64.0.3` | A diff and confirm; it resolves from a client with no restart (US13) |
| H6 | `2 s`, change `node.expiry` to an invalid value | Refused before writing. The file is byte-identical (US10, SC-010) |
| H7 | Change it to a valid value and apply | Diff, then backup, then write, then restart offered. Health turns green, and `u` rolls back (US10) |
| H8 | With `policy.mode: file`, edit the policy | `policy check`, diff, write, reload, active (US5) |
| H9 | `2 l`, filter by level `warn` | Live journald lines, and a machine link jumps to that machine (US14) |
| H10 | `2 S`, `c` (doctor), then `b` (backup) | Findings with fixes. The bundle lists the config, policy, records, and an online database copy (US16) |
| H11 | `headsup conf set log.level debug --yes` (no TTY) | Exit 0, the restart is skipped with a notice. `--no-restart` is honoured (US11) |

Container install: `headsup --host` with `[host].container = "headscale"` runs the same table
through `docker exec`.

## 4. Test suite

```bash
make test          # unit + golden + flow tests (fake backend, fake clock)
make test-all      # + race + coverage gate
make golden-update # after intentional view changes
make test-integration  # docker headscale (HEADSCALE_IMAGE_TAG: 0.29.6, or development for main)
```
