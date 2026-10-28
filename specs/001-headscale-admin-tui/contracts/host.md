# Contract: `host.Host`

```go
package host

type Host interface {
    Env() Env                                   // result of Detect()

    // Managed files
    Open(kind FileKind) (*File, error)          // Config | Policy | ExtraRecords | DERPMap(path)
    Plan(f *File, next []byte) (*Change, error) // validate (schema + headscale validator) + diff
    Apply(ctx context.Context, c *Change) (Applied, error) // hash-check, backup, atomic write, effect
    Rollback(ctx context.Context, a Applied) error

    // Service
    Service() ServiceManager                    // may be NoService{}
    // Logs
    Logs(ctx context.Context, q LogQuery) (<-chan LogEntry, error)
    // Live server data (metrics_listen_addr)
    Debug() DebugClient                         // RegistrationCache, Routes, Filter, DERP, Ping, Overview
    Metrics(ctx context.Context) (MetricsSample, error)
    // Read-only DB enrichment
    NodeExtras(ctx context.Context, ids []uint64) (map[uint64]NodeExtras, error)
    // Maintenance
    Backup(ctx context.Context, opt BackupOpts) (BackupManifest, error)
    Doctor(ctx context.Context) ([]Finding, error)
}

type Env struct {
    Hostname      string
    ConfigPath    string            // "" if not found
    Version       semver.Version
    Transport     TransportKind     // UnixHTTP | ExecCLI | ContainerExec | None
    SocketPath    string
    Service       ServiceKind       // Systemd | OpenRC | Container | Process | None
    LogSource     LogSourceKind     // Journald | Container | File | None
    DB            DBKind            // SQLite | Postgres | Unknown
    DebugAddr     string            // from metrics_listen_addr
    PolicyMode    string            // "file" | "database"
    Perms         Perms             // per-capability: Read/Write/Exec + reason + FixCommand
}

type Effect int // Live | Reload | Restart

type Change struct {
    File      *File
    Next      []byte
    Diff      []DiffHunk
    Issues    []Issue           // schema + headscale validator output, with line/col
    Effect    Effect
    Danger    module.Danger     // Restart ⇒ Confirm at minimum; listen addr/db/noise key ⇒ Critical
}

type ServiceManager interface {
    Status(ctx context.Context) (ServiceStatus, error) // state, since, pid, mem, version
    Start(ctx context.Context) error     // caller runs Configtest first; app refuses on failure
    Stop(ctx context.Context) error      // Danger=Critical (type hostname): all clients lose control plane
    Restart(ctx context.Context) error   // Danger=Confirm; caller must have run Plan(Config) or Configtest first
    Reload(ctx context.Context) error    // Danger=Confirm; SIGHUP reloads the policy only
    Configtest(ctx context.Context, path string) ([]Issue, error)
}
```

## Rules

1. `Apply` MUST re-read the target and compare its hash with the one taken at `Open`. On a
   mismatch it returns `ErrChangedOnDisk` along with the new content, so the caller can show a
   three-way diff.
2. `Apply` MUST back up the file before writing, write atomically, keep the original uid, gid,
   and mode, and fsync both the file and its directory.
3. Any `Issue` with severity Error in `Change.Issues` makes `Apply` refuse with `ErrInvalid`.
   The live file is left byte-identical (SC-010).
4. No method runs `sudo`, `su`, `pkexec`, or `doas`. A permission failure returns
   `ErrPermission{Need, FixCommand}`.
5. `NodeExtras` opens the database read-only for each call, or holds one read-only pool. A
   lint test forbids write SQL in `host/db`.
6. Parsers for `/debug/*` and `/metrics` MUST NOT panic on unknown shapes. On failure they
   return `ErrUnsupportedShape`, and the consuming feature turns itself off.
7. Backup and history writes MUST follow these rules:
   - Directories are created with mode 0700 and files with mode 0600.
   - Writing MUST be refused with `ErrInsecureBackupDir{Path, Mode, Owner, FixCommand}` when the
     root is a symlink, is group- or world-accessible, or belongs to another uid.
   - Manifests carry only paths, hashes, ownership, and mode (FR-064).
8. `host/fake` implements everything in memory. It has injectable failures for permission
   errors, changed-on-disk conflicts, configtest failures, and an unhealthy restart.

## Field catalog (`host/hsconfig/catalog.go`)

Each config key has one entry:

```go
type Field struct {
    Key       string        // "node.ephemeral.inactivity_timeout"
    Section   string        // General | Nodes | DNS | DERP | TLS | OIDC | Policy | Database | Logging | Features
    Type      FieldType     // String | Duration | Bool | Int | Enum | List | Map | Path | Secret | Octal
    Default   any
    Since     string        // "0.29" ; Deprecated string; ReplacedBy string ("tls.cert_path" for tls_cert_path on 0.30)
    Effect    Effect        // all config keys: Restart
    Danger    module.Danger // e.g. server_url, listen_addr, database.*, noise.* ⇒ Critical
    Help      string        // one-line description
    Validate  func(any) error
}
```

The catalog drives the settings editor's forms, the deprecation migrations, and the doctor's
checks, and it serves as documentation. It is versioned for 0.29 and 0.30.
