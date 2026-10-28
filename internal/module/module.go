// Package module defines the contract every feature area implements. The app
// drives modules; modules never import each other.
package module

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/store"
	"github.com/tonhe/fathom/internal/ui/styles"
)

// Modes says where a module works.
type Modes int

const (
	ModeAPI     Modes = iota // needs the headscale API
	ModeHost                 // needs host access
	ModeAPIHost              // API, enhanced on the host
	ModeLocal                // client-side only
)

// Danger is how hard an action must be confirmed.
type Danger int

const (
	DangerNone     Danger = iota
	DangerConfirm         // y/N
	DangerCritical        // type the target name
)

// Scope is where an action's key binding is active.
type Scope int

const (
	ScopeGlobal Scope = iota
	ScopeModule
	ScopeSelection // needs a selected row or multi-selection
	ScopeDetail
)

// Rect is the area a module renders into.
type Rect struct{ W, H int }

// Target names what an action affects, for confirm text and the activity log.
type Target struct {
	Kind string // "node", "user", "key", "file", "service"
	ID   string
	Name string
}

// Values are submitted form values keyed by field ID.
type Values map[string]string

// FieldKind is the type of a form field.
type FieldKind int

const (
	FieldText FieldKind = iota
	FieldToggle
	FieldSelect
	FieldDuration
	FieldTags
	FieldSecret
)

// Field is one input in a parameter form.
type Field struct {
	ID          string
	Label       string
	Kind        FieldKind
	Value       string
	Placeholder string
	Options     []string // for FieldSelect
	Help        string
	Required    bool
	Validate    func(string) error
}

// FormSpec describes a parameter form an action needs before it runs.
type FormSpec struct {
	Title  string
	Fields []Field
	Submit string // button label, default "Save"
}

// ToastLevel is the severity of a toast.
type ToastLevel int

const (
	ToastInfo ToastLevel = iota
	ToastSuccess
	ToastWarn
	ToastError
)

// Action is a single user-invokable operation. It is the one source for key
// bindings, the command palette, help, read-only gating, and confirmation.
type Action struct {
	ID       string   // "machines.expire"
	Title    string   // "Expire machine"
	Keys     []string // default bindings
	Scope    Scope
	Mutating bool
	Danger   Danger
	Multi    bool
	Cap      api.Cap
	Needs    host.Need
	Hidden   bool // keep out of the status bar (still in help/palette)
	// Consequence is one line shown in the confirm dialog ("The node must re-authenticate.").
	Consequence string

	// Enabled may return false with a reason shown in a toast.
	Enabled func(Context) (bool, string)
	// Params returns a form to collect before running; nil for none.
	Params func(Context) *FormSpec
	// Targets lists what the action will affect, for confirmation and logs.
	Targets func(Context) []Target
	// Run performs the action. It must not block; do I/O inside the Cmd.
	Run func(Context, Values) tea.Cmd
}

// Context is what the app hands to modules.
type Context interface {
	Backend() api.Backend
	Host() host.Host // nil in remote mode
	Store() *store.Store
	Styles() *styles.Styles
	Icons() icons.Set
	ReadOnly() bool
	HostMode() bool
	ServerURL() string
	// Now is the app clock; modules must use it instead of time.Now so views are deterministic in tests.
	Now() time.Time
	Navigate(moduleID string, args any) tea.Cmd
	Toast(level ToastLevel, msg string) tea.Cmd
	Refresh() tea.Cmd
}

// Module is a feature area with its own view and actions.
type Module interface {
	ID() string
	Title() string
	Icon(icons.Set) string
	Order() int
	Modes() Modes
	Badge(*store.Store) string

	Init(Context) tea.Cmd
	Update(Context, tea.Msg) (Module, tea.Cmd)
	View(Context, Rect) string
	Actions() []Action
	// Capturing reports whether the module currently owns all keys (e.g. a
	// focused text input), so global bindings must not fire.
	Capturing() bool
	// Hints are short key hints shown in the status bar for the current state.
	Hints() []Hint
}

// Hint is a status-bar key hint.
type Hint struct{ Key, Desc string }

// ActionResult is the message an action's Run command must finish with. The
// app turns it into a toast, an activity-log entry, and a targeted refresh.
type ActionResult struct {
	ActionID string
	Err      error
	// Message is the success toast; empty means a generic "Done".
	Message string
	// Refresh lists store slices to reload (store.Slice bits); 0 means none.
	Refresh store.Slice
	// Silent suppresses the success toast (e.g. the module shows its own result).
	Silent bool
}

// NavigateMsg is sent to a module when it becomes active via Navigate.
type NavigateMsg struct{ Args any }

// FilterArgs asks a list module to apply a filter query on navigation.
type FilterArgs struct{ Query string }

// SelectArgs asks a module to select an item by ID.
type SelectArgs struct{ ID uint64 }
