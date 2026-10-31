# Contract: Modules and Actions

```go
package module

type Module interface {
    ID() string                      // "machines" — stable, used in config + Navigate
    Title() string                   // "Machines"
    Icon(s icons.Set) string
    Order() int                      // position within its sidebar section
    Badge(st *store.Store) string    // "" or e.g. "2" (pending routes)

    Init(ctx Context) tea.Cmd
    Update(ctx Context, msg tea.Msg) (Module, tea.Cmd)
    View(ctx Context, area Rect) string
    Actions() []Action               // single source for keys, palette, help, CLI
    Focusable() bool                 // false while a text input inside owns keys
    Modes() Modes                    // API | Host | APIHost | Local
}

// Optional: the sidebar section. Without it, ModeHost modules go under
// Server, ModeLocal under App, the rest under Tailnet.
type Sectioned interface{ Section() Section } // SectionTailnet | SectionServer | SectionApp

// Optional (app package): the module's "g x" chord letter, e.g. "m".
type Chorder interface{ Chord() string }

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

| ID | Keys | Badge | Main actions |
|---|---|---|---|
| dashboard | `1 h` · `g h` | — | refresh, jump to issue cards |
| machines | `1 m` · `g m` | — | open, rename `r`, tags `t`, expire `e`, no-expiry `E`, routes `a`, delete `D`, copy IP `y`, copy name `Y`, select `space`, select all `ctrl+a` |
| users | `1 u` · `g u` | — | create `n`, rename `r`, delete `D`, show machines `enter`, show keys `k` |
| routes | `1 r` · `g r` | pending count | approve `a`, revoke `x`, approve all for node `A` |
| pending | `1 p` · `g p` | queue size | approve `a`, reject `x`, register by key `p`, new pre-auth key `n` |
| keys | `1 k` · `g k` | expiring | tab between pre-auth and API, new `n`, expire `e`, delete `D`, show command `c`, QR `q` |
| policy | `1 a` · `g a` | — | edit `e`, `$EDITOR` `E`, check `v`, diff `d`, apply `ctrl+s`, history `h`, explore `x`, preview `p` |
| server | `2 S` · `g S` | doctor findings | capability matrix, doctor `c`, backup `b`, backups list, upgrade check `U`, backfill IPs |
| serverconfig | `2 s` · `g s` | unsaved / restart needed | section nav, edit field `enter`, `$EDITOR` `E`, migrate deprecated `m`, diff `d`, apply `ctrl+s`, rollback `u` |
| dns | `2 d` · `g d` | restart needed | MagicDNS toggle, base domain, nameservers, split DNS, search domains, records (add `n`, edit, delete `D`), apply `ctrl+s` |
| derp | `2 D` · `g D` | — | embedded server form, map URLs and files, region editor, live map |
| service | `2 l` · `g l` | ● state | start, stop, restart `X` (confirm), reload `H`, log follow `f`, level filter `l`, search `/`, pause `space`, jump to error `n` |
| activity | `3 v` · `g v` | — | filter, export |
| prefs | `3 ,` · `g ,` · `,` | — | theme, icons, density, columns, refresh, profiles |

User-facing labels: the `serverconfig` module is shown as **"Server Settings"** (`g s`). The
`prefs` module is shown as **"Preferences"** (`,`). The bare word "Settings" is never used on
its own in the UI, help, or docs.

Module jump keys: the sidebar groups pages into sections, each with a digit: `1` Tailnet
(dashboard, machines, users, routes, pending, keys, policy), `2` Server (server, serverconfig,
dns, derp, service) and `3` App (activity, prefs). A module declares its section with
`Section()`; within a section pages are ordered by `Order()`. Host-only (`ModeHost`) pages are
left out of the sidebar, the tab bar, section jumps and `]`/`[` in remote mode; their chords and
palette entries still open them with a host-mode explanation.

Pressing a section digit starts a two-key jump: the sidebar highlights that section and shows
each page's key in place of its icon, and the status bar shows a which-key line such as
`1 Tailnet  h Dashboard  m Machines  …  esc cancel` (also with the sidebar collapsed or in the
tab-bar layout, where the tab bar lists just that section's pages). The second key is the
page's letter: the letter of its live `g` chord, else `Chord()`, else the first free letter of
its title, assigned in sidebar order so clashes within a section resolve deterministically.
`enter`, or the section digit again (`1 1`), opens the section's first page; another digit
`1`–`9` opens the nth page. `esc` or any other key cancels the jump and is not passed to the
page. There is no timeout. Section digits are the remappable actions `app.section.tailnet`,
`app.section.server` and `app.section.app`.

Every module also has a `g`-chord, such as `g m` for machines, `g d` for DNS, and `g l` for
service and logs, so jump keys never collide with in-module single-letter actions. The sidebar
shows each chord dimmed at the right edge when the row has room. Clicking a section header
opens its first page. The help screen's Navigation sections list all of it, generated from the
same data as the sidebar.

The machines detail also gains, in host mode: ping `p`, plus OS, client, DERP, and endpoints
in the detail view.

## Global keys

These can be remapped under `[keys]` in config:

- `:` and `ctrl+p`: palette
- `?`: help
- `/`: filter
- `tab`, `shift+tab`: move focus
- `1`, `2`, `3`: start a section jump (Tailnet, Server, App), then a page key
- `g` + letter: go straight to a page; `]` / `[`: next / previous page
- `ctrl+b`: toggle sidebar
- `T`: theme picker
- `P`: profile switcher
- `R` and `ctrl+r`: refresh
- `q` and `ctrl+c`: quit, with a guard if there are unsaved policy edits
- `j`/`k`/`g`/`G`/`ctrl+d`/`ctrl+u`: movement
