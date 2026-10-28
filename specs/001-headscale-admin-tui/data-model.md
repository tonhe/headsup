# Data Model: Headsup

These are the domain types in `internal/api`. They do not depend on the wire format: the
`httpapi` package maps JSON (camelCase, with uint64 encoded as strings) onto them.

## Node

| Field | Type | Notes |
|---|---|---|
| ID | uint64 | |
| Name | string | Hostname reported by the client |
| GivenName | string | Admin-set name; editable through rename. It is the display name. |
| User | UserRef | Owner (ID and Name). Read-only: the API has no move operation. |
| IPAddresses | []netip.Addr | v4 first, then v6 |
| Tags | []string | `tag:*`. Set-tags replaces the whole list. |
| Online | bool | |
| LastSeen | time.Time | Zero means never |
| Expiry | *time.Time | nil or zero means expiry is disabled |
| CreatedAt | time.Time | |
| RegisterMethod | enum | `cli`, `authkey`, or `oidc` |
| PreAuthKey | *PreAuthKeyRef | The key it joined with, if any |
| MachineKey, NodeKey, DiscoKey | string | Shown as short fingerprints |
| AvailableRoutes | []netip.Prefix | Advertised by the node |
| ApprovedRoutes | []netip.Prefix | Approved by an admin |
| SubnetRoutes | []netip.Prefix | Approved and being served |

Derived fields:

- `IsExitNode`: AvailableRoutes contains both `0.0.0.0/0` and `::/0`.
- `PendingRoutes`: Available minus Approved.
- `Expired`: Expiry is in the past.
- `ExpiresSoon`: Expiry is less than `cfg.expiry_warn` away, 7 days by default.
- `Status`: one of `Online`, `Idle` (offline and last seen under 24h ago), `Offline`, or
  `Expired`.

Validation:

- GivenName must be a DNS label: lowercase, `[a-z0-9-]`, 1 to 63 characters. The UI suggests a
  sanitized form.
- Each tag must match `^tag:[a-z0-9-]+$`.

State transitions:

```text
pending ─register/approve─▶ active ─expire─▶ expired ─client re-auth─▶ active
active ─delete─▶ (gone)
```

## User

| Field | Type | Notes |
|---|---|---|
| ID | uint64 | |
| Name | string | Unique. Rename keys on ID. |
| DisplayName | string | |
| Email | string | |
| Provider | string | `""` for local, or `oidc` |
| ProviderID | string | |
| ProfilePicURL | string | Not rendered; shown as a link only |
| CreatedAt | time.Time | |

Derived: `NodeCount` and `KeyCount`, computed from the store.

Delete rule: blocked in the UI while `NodeCount > 0`.

## PreAuthKey

| Field | Type | Notes |
|---|---|---|
| ID | uint64 | |
| Key | Secret | Masked as `hskey-auth-…a1b2` |
| User | *UserRef | nil for tags-only keys (0.30 and later) |
| Reusable, Ephemeral, Used | bool | |
| Expiration | time.Time | |
| CreatedAt | time.Time | |
| ACLTags | []string | |

Derived: `State`, which is one of `Active`, `Used` (single-use and already used), or
`Expired`.

## APIKey

| Field | Type | Notes |
|---|---|---|
| ID | uint64 | Display only. Do not use as an identifier, because IDs can change. |
| Prefix | string | The identifier for expire and delete |
| Expiration, CreatedAt, LastSeen | time.Time | |

Derived: `IsSelf` is true when the prefix matches the key of the active profile.

## Policy

| Field | Type | Notes |
|---|---|---|
| Raw | string | The original HuJSON text, preserved byte-for-byte |
| UpdatedAt | time.Time | Used for optimistic concurrency |
| Doc | *policy.Document | Parsed form: Groups, TagOwners, Hosts, ACLs, Grants, SSH, AutoApprovers, Tests |

## PendingRegistration (0.30 and later, from the auth flow)

| Field | Type | Notes |
|---|---|---|
| AuthID | string | Taken from a pasted URL or key |
| RequestedAt | time.Time | |
| Hostname | string | When known |

The queue has two sources, merged by AuthID:

- **Host mode**: `/debug/registration-cache` reports only counters (`current_len` and so on)
  in 0.29.6 and in main, not the entries. Headsup shows that count ("headscale reports N
  devices waiting") and uses the entries only if a future headscale exposes them.
- **Remote mode**: headscale's API has no list endpoint, so entries come from registration keys
  or URLs the admin pastes in.

An entry leaves the queue on approve or reject, or when it disappears from the registration
cache.

## Capabilities

A set of flags:

```text
HealthEndpoint, PolicyCheck, PolicyWritable(tri-state), AuthApproval,
PreAuthKeyDelete, APIKeyDelete, OAuthClients, ProblemJSON, TagOnlyKeys, BackfillIPs
```

## Client-side entities

- **Profile**: `name`, `url`, credential source, `ca_file`, `insecure_skip_verify`,
  `read_only`, and an optional per-profile `theme`.
- **Theme**: `Name`, `Slug`, `Light`, `Base00`–`Base0F`, and an optional Roles override.
- **ActivityEntry**: `time`, `profile`, `action_id`, `target` (kind, id, name), `params`
  (redacted), `result` (ok or the error kind and message), and `duration_ms`.
- **PolicyRevision**: `profile`, `fetched_at`, `server_updated_at`, `sha256`, `path`, and
  `source` (`server`, `pre-apply`, or `applied`).
- **Event** (watch): `kind`, `at`, and `subject`.

## Host entities

- **Env**: see `contracts/host.md`.
- **NodeExtras** (database, read-only): `OS`, `OSVersion`, `ClientVersion` (IPNVersion),
  `Hostname`, `PreferredDERP` (region ID), `DERPLatency` (map of region to ms), `Endpoints`
  ([]netip.AddrPort), and `RequestTags`.
- **NodeRouteState** (from `/debug/routes`): for each prefix, the primary node ID and the
  candidates.
- **File**: `Kind`, `Path`, `UID`, `GID`, `Mode`, `SHA256`, `Raw`, `Model` (typed), and
  `Effect`.
- **Change**: see `contracts/host.md`. Changes always carry `Diff` and `Issues`.
- **BackupManifest**: `ID` (timestamp), `Files` (path, sha256, uid, gid, mode), `DB` (kind,
  path, sha256, method `sqlite-backup-api` or `pg_dump-instructions`), `HeadscaleVersion`, and
  `HeadsupVersion`.
- **DNSConfig**: `MagicDNS` bool, `BaseDomain` string (must differ from the `server_url` host),
  `OverrideLocalDNS` bool, `Global` []string (IPs or DoH URLs), `Split` map[domain][]string,
  `SearchDomains` []string, and `ExtraRecords` []Record or `ExtraRecordsPath` (mutually
  exclusive).
- **Record**: `Name` (FQDN), `Type` (one of `A` or `AAAA`), and `Value` (an IP of the matching
  family).
- **DERPMap**: `Regions` map[int]Region. A **Region** has `RegionID` (1–999 for custom),
  `RegionCode`, `RegionName`, and `Nodes` []Node (Name, RegionID, HostName, IPv4, IPv6,
  STUNPort, DERPPort, InsecureForTests). The embedded server settings come from `derp.server.*`.
- **LogEntry**: `Time`, `Level` (trace…fatal), `Msg`, `Fields` map, and `NodeRefs` []uint64.
- **Finding** (doctor): `ID`, `Severity` (info, warn, error), `Title`, `Detail`, `Fix`
  (command or action ID).
- **MetricsSample**: `At`, `Nodes`, `MapResponsesPerSec`, `HeapBytes`, and `Goroutines`.
