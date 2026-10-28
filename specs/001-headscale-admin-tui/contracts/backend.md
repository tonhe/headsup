# Contract: `api.Backend`

```go
package api

type Backend interface {
    // Meta
    Connect(ctx context.Context) (ServerInfo, error) // version, health, capabilities
    Capabilities() Capabilities

    // Nodes
    ListNodes(ctx context.Context, f NodeFilter) ([]Node, error)       // f.User optional
    GetNode(ctx context.Context, id uint64) (Node, error)
    RegisterNode(ctx context.Context, user, key string) (Node, error)
    RenameNode(ctx context.Context, id uint64, newName string) (Node, error)
    SetTags(ctx context.Context, id uint64, tags []string) (Node, error)
    SetApprovedRoutes(ctx context.Context, id uint64, routes []netip.Prefix) (Node, error)
    ExpireNode(ctx context.Context, id uint64, opt ExpireOpts) (Node, error) // At / DisableExpiry
    DeleteNode(ctx context.Context, id uint64) error
    BackfillIPs(ctx context.Context) ([]string, error)

    // Users
    ListUsers(ctx context.Context, f UserFilter) ([]User, error)
    CreateUser(ctx context.Context, u NewUser) (User, error)
    RenameUser(ctx context.Context, id uint64, newName string) (User, error)
    DeleteUser(ctx context.Context, id uint64) error

    // Pre-auth keys
    ListPreAuthKeys(ctx context.Context, f KeyFilter) ([]PreAuthKey, error)
    CreatePreAuthKey(ctx context.Context, k NewPreAuthKey) (PreAuthKey, error)
    ExpirePreAuthKey(ctx context.Context, id uint64) error
    DeletePreAuthKey(ctx context.Context, id uint64) error

    // API keys
    ListAPIKeys(ctx context.Context) ([]APIKey, error)
    CreateAPIKey(ctx context.Context, expiration time.Time) (Secret, error)
    ExpireAPIKey(ctx context.Context, prefix string) error
    DeleteAPIKey(ctx context.Context, prefix string) error

    // Policy
    GetPolicy(ctx context.Context) (Policy, error)
    CheckPolicy(ctx context.Context, raw string) error // *api.Error{Kind: Invalid, Line, Col}
    SetPolicy(ctx context.Context, raw string) (Policy, error)

    // Interactive auth
    AuthRegister(ctx context.Context, authID, user string) error
    AuthApprove(ctx context.Context, authID string) error
    AuthReject(ctx context.Context, authID string) error
}
```

Contract rules:

- Every method honours `ctx` cancellation. The default timeout is 10s.
- Errors are always `*api.Error{Kind, Message, Hint, Status, Line, Col}`.
- A call the server does not support returns `Kind=Unsupported` without making a network call.
- `SetApprovedRoutes` and `SetTags` replace the whole list. Callers merge.
- No method logs secrets. `Secret.String()` returns the masked form.

## Endpoint and capability matrix (REST `/api/v1`)

| Backend method | HTTP | Cap gate | Module / Action |
|---|---|---|---|
| Connect | GET /health, GET /version | — | app |
| ListNodes | GET /node?user= | — | machines, dashboard, routes |
| GetNode | GET /node/{id} | — | machines.detail |
| RegisterNode | POST /node/register?user=&key= | — | pending.register |
| RenameNode | POST /node/{id}/rename/{name} | — | machines.rename |
| SetTags | POST /node/{id}/tags | — | machines.tags |
| SetApprovedRoutes | POST /node/{id}/approve_routes | — | routes.approve / revoke, machines.routes |
| ExpireNode | POST /node/{id}/expire | DisableExpiry flag (0.30+) | machines.expire / noexpire |
| DeleteNode | DELETE /node/{id} | — | machines.delete |
| BackfillIPs | POST /node/backfillips?confirmed=true | BackfillIPs | server.backfill |
| ListUsers | GET /user | — | users |
| CreateUser | POST /user | — | users.create |
| RenameUser | POST /user/{id}/rename/{name} | — | users.rename |
| DeleteUser | DELETE /user/{id} | — | users.delete |
| ListPreAuthKeys | GET /preauthkey | — | keys.preauth |
| CreatePreAuthKey | POST /preauthkey | — | keys.preauth.create, pending.newkey |
| ExpirePreAuthKey | POST /preauthkey/expire | — | keys.preauth.expire |
| DeletePreAuthKey | DELETE /preauthkey?id= | PreAuthKeyDelete | keys.preauth.delete |
| ListAPIKeys | GET /apikey | — | keys.api |
| CreateAPIKey | POST /apikey | — | keys.api.create |
| ExpireAPIKey | POST /apikey/expire | — | keys.api.expire |
| DeleteAPIKey | DELETE /apikey/{prefix} | APIKeyDelete | keys.api.delete |
| GetPolicy | GET /policy | — | policy |
| CheckPolicy | POST /policy/check | PolicyCheck | policy.validate |
| SetPolicy | PUT /policy | PolicyWritable | policy.apply / rollback |
| AuthRegister / Approve / Reject | POST /auth/{register,approve,reject} | AuthApproval | pending |

Not available through the API:

- DNS, DERP, OIDC, and other server config: shown as "host mode", and provided by
  `host.Host`.
- Moving a node to another owner, flow logs, posture, and Funnel: shown as unsupported by
  headscale. OAuth clients (0.30, through `/api/v2/tailnet/-/keys`) are deferred to v1.1, as in spec
FR-041. The capability matrix shows them as "planned".

## Backend implementations

| Backend | Used when | Auth |
|---|---|---|
| `httpapi` over TCP | remote mode (0.29 gateway or 0.30) | Bearer API key |
| `httpapi` over a unix socket | host mode on 0.30 and later | none (local trust) |
| `execcli` | host mode on 0.29.x; containers through `docker/podman exec` | none (runs the local CLI) |
| `fake` | tests, `--demo` | — |

`execcli` maps both CLI JSON shapes: 0.29 protobuf JSON and 0.30 camelCase with string IDs.
`AuthApprove`, `AuthReject`, and `CheckPolicy` map to `headscale auth …` and
`headscale policy check`. Every backend MUST pass `apitest.RunContract`.

Some extras are not part of `Backend`, because they come from `host.Host`. When a host is
present, the store merges them into `Node`:

- host_info: OS, client version, DERP preference
- endpoints
- primary-route flags
- registration-cache entries

## `fake.Backend`

- An in-memory implementation seeded from `testdata/seed/*.json`.
- Supports latency and error injection (`fake.WithLatency`, `fake.FailNext(method, kind)`).
- Used by unit and golden tests and by `headsup --demo`, a zero-setup demo mode for the README
  GIF and for trying the app.
