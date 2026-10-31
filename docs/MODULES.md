# Writing a module

Every page in headsup (Machines, DNS, Activity and the rest) is a module. A
module lives in one directory under `internal/modules/`, registers itself
from `init()`, and costs one import line in `internal/modules/all/all.go`.
Nothing else in the app changes when you add one.

The contract is in [`specs/001-headscale-admin-tui/contracts/module.md`](../specs/001-headscale-admin-tui/contracts/module.md).
The Go source of truth is `internal/module/module.go`.

## The rules

- Modules never import other modules. To open another page, return
  `ctx.Navigate("machines", args)`. No test enforces this yet, so reviewers
  check it.
- Modules don't touch the filesystem or spawn processes. API calls go through
  `ctx.Backend()`, host work through `ctx.Host()` (nil in remote mode).
- `Run` never blocks. It returns a `tea.Cmd`, and the I/O happens inside it.
- Modules use `ctx.Now()`, never `time.Now()`, so views are deterministic in
  tests.
- No colour literals. Styles come from `ctx.Styles()`, which is built from the
  theme's roles (see [THEMES.md](THEMES.md)). A test fails on any
  `lipgloss.Color(` or quoted `#rrggbb` outside `internal/theme`.
- The product name lives in `internal/brand`. Use `brand.Name` or
  `brand.Display` in code.

## The interface

```go
type Module interface {
	ID() string               // "machines": stable, used in config, Navigate and action IDs
	Title() string            // "Machines"
	Icon(icons.Set) string
	Order() int               // position within its sidebar section
	Modes() Modes             // ModeAPI | ModeHost | ModeAPIHost | ModeLocal
	Badge(*store.Store) string // "" or a short count shown in the sidebar

	Init(Context) tea.Cmd
	Update(Context, tea.Msg) (Module, tea.Cmd)
	View(Context, Rect) string
	Actions() []Action
	Capturing() bool          // true while a text input owns every key
	Hints() []Hint            // key hints for the status bar, for the current state
}
```

Three optional interfaces:

- `Section() module.Section` puts the page under `SectionTailnet` (`1`),
  `SectionServer` (`2`) or `SectionApp` (`3`). Without it, `ModeHost` modules
  go under Server, `ModeLocal` under App and the rest under Tailnet.
- `Chord() string` gives the page its `g` chord (`"m"` makes `g m`). The same
  letter becomes its key in a section jump (`1 m`), unless another page in
  the section already took it.
- `JumpKeys() []string` adds extra global keys that open the page.
  Preferences uses it for `,`.

`Modes` decides where the page appears. `ModeHost` pages are left out of the
sidebar, the tab bar, section jumps and `]`/`[` in remote mode. Their chords
and palette entries still work and explain that the page needs host mode.

`Badge` is drawn next to the sidebar entry. Keep it to a few characters: the
Routes page shows the number of routes waiting for approval.

`Capturing` matters. While it returns true, the app hands every key to your
`Update`, so typing `q` into a filter box doesn't quit. Return true only while
a text input has focus.

## Actions

`Actions()` is the single source for everything a user can do on the page.
From that one list the app builds the key bindings, the `:` palette, the
help screen (`?`), read-only gating, confirmations, the activity log and the
CLI.

```go
module.Action{
	ID:       "users.rename",           // <module id>.<verb>, remappable under [keys]
	Title:    "Rename user",            // palette and help text
	Keys:     []string{"r"},            // default bindings, active on this page
	Mutating: true,                     // blocked in read-only mode, logged to Activity
	Danger:   module.DangerConfirm,     // DangerNone | DangerConfirm (y/N) | DangerCritical (type the name)
	Cap:      api.CapNone,              // server capability it needs
	Needs:    host.NeedNone,            // host permission it needs
	Enabled:  hasCursor,                // false plus a reason disables it, with that reason shown
	Params:   renameForm,               // a form to fill before Run, or nil
	Targets:  selectedUser,             // what it affects, for confirm text and the log
	Run:      rename,                   // returns a tea.Cmd that ends in a module.ActionResult
}
```

The app runs the gate, so modules can't skip it: enabled, read-only, capability and
host permission, in that order (`internal/app/gate.go`). A module can't skip
a confirmation. A mutating action with no form is confirmed at least `y/N`
even if you forget to set `Danger`. When a host permission is missing, the
app shows the reason and the exact fix command from host detection.

`Run`'s command must finish with a `module.ActionResult` carrying the same
`ActionID`. The app turns it into a toast, an Activity entry and a refresh of
the store slices in `Refresh` (`store.SliceUsers` and so on). Fill `Files`
and `BackupID` when the action wrote managed files on the host, so the
Activity log records them.

Mark rarely used actions `Hidden: true` to keep them out of the status bar.
They stay in the palette and help.

### CLI verbs

The CLI in `internal/cli` runs the same actions headlessly: it gates, fills
the form from flags, confirms (or requires `--yes`), runs and logs. Adding a
verb means a handler in `internal/cli` that calls the action by ID, for
example `call{id: "users.rename", values: module.Values{"name": newName}}`,
plus an entry in the verb tree in `router.go` and in
[`contracts/cli.md`](../specs/001-headscale-admin-tui/contracts/cli.md).

## A small example

A Tags page that lists every tag in use and opens Machines filtered to the
selected one:

```go
// Package tags lists the ACL tags in use, with a jump to their machines.
package tags

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/pane"
)

func init() { registry.Register(func() module.Module { return &Model{} }) }

// Model is the tags module.
type Model struct {
	tags   []string
	cursor int
}

func (m *Model) ID() string                { return "tags" }
func (m *Model) Title() string             { return "Tags" }
func (m *Model) Icon(ic icons.Set) string  { return ic.Tag }
func (m *Model) Order() int                { return 27 }
func (m *Model) Modes() module.Modes       { return module.ModeAPI }
func (m *Model) Section() module.Section   { return module.SectionTailnet }
func (m *Model) Chord() string             { return "t" }
func (m *Model) Badge(*store.Store) string { return "" }
func (m *Model) Capturing() bool           { return false }
func (m *Model) Hints() []module.Hint      { return []module.Hint{{Key: "enter", Desc: "machines"}} }

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.rebuild(ctx.Store())
	return nil
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		m.rebuild(ctx.Store())
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.tags)-1))
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		}
	}
	return m, nil
}

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	w, _ := pane.Inner(r.W, r.H)
	lines := make([]string, 0, len(m.tags))
	for i, t := range m.tags {
		l := "  " + t
		if i == m.cursor {
			l = s.Accent.Render(ic.Arrow + " " + t)
		}
		lines = append(lines, pane.Fit(l, w))
	}
	return pane.Render(s, "Tags", strings.Join(lines, "\n"), r.W, r.H, true)
}

func (m *Model) Actions() []module.Action {
	return []module.Action{{
		ID: "tags.machines", Title: "Show machines with this tag", Keys: []string{"enter"},
		Enabled: func(module.Context) (bool, string) {
			if len(m.tags) == 0 {
				return false, "No tags in use."
			}
			return true, ""
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			return ctx.Navigate("machines", module.FilterArgs{Query: m.tags[m.cursor]})
		},
	}}
}

func (m *Model) rebuild(st *store.Store) {
	m.tags = m.tags[:0]
	for _, n := range st.Nodes {
		for _, t := range n.Tags {
			if !slices.Contains(m.tags, t) {
				m.tags = append(m.tags, t)
			}
		}
	}
	slices.Sort(m.tags)
	m.cursor = min(m.cursor, max(0, len(m.tags)-1))
}
```

Then add `_ "github.com/tonhe/headsup/internal/modules/tags"` to
`internal/modules/all/all.go`. The page shows up under Tailnet with `1 t` and
`g t`, and its action appears in help and the palette. Users can turn it off
with `[modules] disabled = ["tags"]`.

For lists, use the shared components in `internal/ui` instead of hand-rolled
loops: `table` (sorting, multi-select, column priorities), `filter` (the `/`
query bar with `tag:`, `user:` and `is:` terms), `pane`, `empty`, `kv`,
`form` and `confirm`. The Users module (`internal/modules/users`) is a good
model to copy: a filtered table, a detail pane, forms and a blocked-delete
notice in under 600 lines.

## Testing

Coverage floors per package are in `scripts/coverage-floors.txt` and only
go up. Add a floor for a new module's package once its tests are in. Tests
come in three kinds.

**Logic.** Table-driven tests with the stdlib `testing` package. Use
`api/fake` (`fake.Demo()` gives a realistic seeded tailnet,
`fake.WithClock`, `fake.WithCaps`, `fake.FailNext` for errors) and
`host/fake` for host features. A small `module.Context` over the fake backend
is enough for most module tests. See `internal/modules/users/ctx_test.go`.

**Goldens.** Render a view and compare it with a snapshot:

```go
func TestViewGolden(t *testing.T) {
	// ... build the module and context ...
	uitest.Golden(t, m.View(ctx, module.Rect{W: 98, H: 37}))
}
```

`uitest.Golden` strips ANSI codes, so a golden captures layout and works with any
theme. It compares with `testdata/<TestName>.golden`. Run
`make golden-update` to write or refresh them, then read the diff before you
commit it. Test at 80x24 or close to it as well as a wide size: every page has
to work at 80x24.

**Flows.** `uitest.Driver` runs the whole app headlessly to test a user
journey: keys in, screen out.

```go
d := uitest.NewDriver(t, app.New(app.Options{
	Backend: fake.Demo(fake.WithClock(clock)),
	Modules: []module.Module{&tags.Model{}, &machinesStub{}},
	Now:     clock, Theme: "tokyo-night",
}), 120, 40)
d.Key("1", "t")
d.Want("tag:web")
d.Key("enter")
d.Want("MACHINES STUB")
```

The driver runs the model and every command inside a `testing/synctest`
bubble with a fake clock. Each `Key`, `Type`, `Send` or `Do` returns once the
model has settled, so there are no sleeps and no polling. Commands still
waiting on a timer after the operation's budget (toast dismissal, refresh
ticks, a log stream) are parked and kept, and `Advance(d)` moves the
clock on to deliver them. Cancel anything that blocks on a channel, such as
a log stream, in an `OnClose` hook. Because modules can't import each other,
flow tests stand in for other pages with a stub that records the `Navigate`
it receives. `internal/modules/service/flow_test.go` has a complete example.

## Docs

The actions table in [CAPABILITIES.md](CAPABILITIES.md) is generated from
`Actions()`. After adding or changing actions, run `make docs` and commit the
result. `make test-all` fails while the doc is stale.
