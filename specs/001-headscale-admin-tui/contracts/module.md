# Contract: Modules and Actions

```go
package module

type Module interface {
    ID() string                      // "machines" — stable, used in config + Navigate
    Title() string                   // "Machines"
    Icon(s icons.Set) string
    Order() int                      // sidebar position
    Badge(st *store.Store) string    // "" or e.g. "2" (pending routes)

    Init(ctx Context) tea.Cmd
    Update(ctx Context, msg tea.Msg) (Module, tea.Cmd)
    View(ctx Context, area Rect) string
    Actions() []Action               // single source for keys, palette, help, CLI
    Focusable() bool                 // false while a text input inside owns keys
    Modes() Modes                    // API | Host | APIHost | Local
}

type Context interface {
    Backend() api.Backend
    Host() host.Host                 // nil in remote mode
    Store() *store.Store
    Theme() *styles.Styles
    Icons() icons.Set
    Keys() keys.Map
    Profile() config.Profile
    ReadOnly() bool
    Navigate(moduleID string, args any) tea.Cmd
    Toast(level toast.Level, msg string) tea.Cmd
    Confirm(req confirm.Request) tea.Cmd
    Form(f form.Spec) tea.Cmd
    Activity() activity.Logger
}

type Danger int // None | Confirm | Critical(type target name)

type Action struct {
    ID        string            // "machines.expire" — key for remapping + palette + CLI
    Title     string            // "Expire machine"
    Keys      []string          // default bindings, e.g. {"e"}
    Scope     Scope             // Global | Module | Selection | Detail
    Mutating  bool
    Danger    Danger
    Multi     bool              // works on multi-selection
    Cap       api.Cap           // required capability (0 = none)
    Needs     host.Need         // required host permission (0 = none) → disabled w/ FixCommand
    Enabled   func(Context) (bool, string)
    Params    func(Context) *form.Spec      // nil = no params
    Target    func(Context) []Target        // for confirm text + activity log
    Run       func(Context, form.Values) tea.Cmd
}

// registry
func Register(m func() Module)   // called from init()
func All(cfg config.Modules) []Module
```

## Rules

- Modules MUST NOT import other modules. Cross-module navigation goes through `Navigate`.
- `Run` MUST NOT perform I/O on the calling goroutine. It returns a `tea.Cmd`.
- The app, not the module, handles the confirm, read-only, and capability gates. A module
  cannot skip confirmation.
- Every `Action` with `Keys` appears in help and in the palette. Every action whose ID is in
  the CLI allowlist also gets a CLI verb.

## Built-in modules

| ID | Key | Badge | Main actions |
|---|---|---|---|
| dashboard | 1 | — | refresh, jump to issue cards |
| machines | 2 | — | open, rename `r`, tags `t`, expire `e`, no-expiry `E`, routes `a`, delete `D`, copy IP `y`, copy name `Y`, select `space`, select all `ctrl+a` |
| users | 3 | — | create `n`, rename `r`, delete `D`, show machines `enter`, show keys `k` |
| routes | 4 | pending count | approve `a`, revoke `x`, approve all for node `A` |
| pending | 5 | queue size | approve `a`, reject `x`, register by key `p`, new pre-auth key `n` |
| keys | 6 | expiring | tab between pre-auth and API, new `n`, expire `e`, delete `D`, show command `c`, QR `q` |
| policy | 7 | — | edit `e`, `$EDITOR` `E`, check `v`, diff `d`, apply `ctrl+s`, history `h`, explore `x`, preview `p` |
| dns | `g d` | restart needed | MagicDNS toggle, base domain, nameservers, split DNS, search domains, records (add `n`, edit, delete `D`), apply `ctrl+s` |
| derp | `g D` | — | embedded server form, map URLs and files, region editor, live map |
| serverconfig | `g s` | unsaved / restart needed | section nav, edit field `enter`, `$EDITOR` `E`, migrate deprecated `m`, diff `d`, apply `ctrl+s`, rollback `u` |
| service | `g l` | ● state | start, stop, restart `X` (confirm), reload `H`, log follow `f`, level filter `l`, search `/`, pause `space`, jump to error `n` |
| server | 8 | doctor findings | capability matrix, doctor `c`, backup `b`, backups list, upgrade check `U`, backfill IPs |
| activity | 9 | — | filter, export |
| prefs | `,` or `g ,` | — | theme, icons, density, columns, refresh, profiles |

User-facing labels: the `serverconfig` module is shown as **"Server Settings"** (`g s`). The
`prefs` module is shown as **"Preferences"** (`,`). The bare word "Settings" is never used on
its own in the UI, help, or docs.

Module jump keys: `1`–`9` go to the first nine modules in sidebar order. Every module also has
a `g`-chord, such as `g m` for machines, `g d` for DNS, and `g l` for service and logs, so jump
keys never collide with in-module single-letter actions. The help screen lists them all.

The machines detail also gains, in host mode: ping `p`, plus OS, client, DERP, and endpoints
in the detail view.

## Global keys

These can be remapped under `[keys]` in config:

- `:` and `ctrl+p`: palette
- `?`: help
- `/`: filter
- `tab`, `shift+tab`: move focus
- `ctrl+b`: toggle sidebar
- `T`: theme picker
- `P`: profile switcher
- `R` and `ctrl+r`: refresh
- `q` and `ctrl+c`: quit, with a guard if there are unsaved policy edits
- `j`/`k`/`g`/`G`/`ctrl+d`/`ctrl+u`: movement
