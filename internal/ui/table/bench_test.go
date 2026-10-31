package table_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/icons"
	_ "github.com/tonhe/headsup/internal/modules/all"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/ui/filter"
	"github.com/tonhe/headsup/internal/ui/table"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

// perfNodes is the tailnet size the frame budget is measured at (T170).
const perfNodes = 2000

// frameBudget is one frame at 60 Hz.
const frameBudget = 16 * time.Millisecond

var perfNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// bigSeed repeats the demo seed's nodes until there are n of them, each with
// its own ID, name and addresses.
func bigSeed(n int) fake.Seed {
	s := fake.DemoSeed()
	base := s.Nodes
	s.Nodes = make([]fake.SeedNode, 0, n)
	for i := range n {
		sn := base[i%len(base)]
		sn.ID = uint64(i + 1)
		sn.Name = fmt.Sprintf("%s-%04d", sn.Name, i)
		if sn.GivenName != "" {
			sn.GivenName = fmt.Sprintf("%s-%04d", sn.GivenName, i)
		}
		sn.IPv4 = fmt.Sprintf("100.64.%d.%d", i/250, i%250+1)
		sn.IPv6 = fmt.Sprintf("fd7a:115c:a1e0::%x", i+1)
		sn.MachineKey = fmt.Sprintf("mkey:%064x", i+1)
		sn.NodeKey = fmt.Sprintf("nodekey:%064x", i+1)
		sn.DiscoKey = fmt.Sprintf("discokey:%064x", i+1)
		s.Nodes = append(s.Nodes, sn)
	}
	return s
}

// harness runs the app synchronously on the real clock so frame times are
// real. Commands run on goroutines; their results are fed back until the
// caller's condition holds or the app goes quiet. Commands still waiting
// (refresh ticks, cursor blinks) are left behind and their results dropped.
type harness struct {
	m   tea.Model
	out chan tea.Msg
}

func newHarness(tb testing.TB, w, h int) *harness {
	tb.Helper()
	clock := func() time.Time { return perfNow }
	m := app.New(app.Options{
		Backend:   fake.New(bigSeed(perfNodes), fake.WithClock(clock)),
		Modules:   registry.All(nil),
		Now:       clock,
		Theme:     "tokyo-night",
		Icons:     "unicode",
		ServerURL: "https://hs.example.com",
		Refresh:   time.Hour,
	})
	hs := &harness{m: m, out: make(chan tea.Msg, 4096)}
	hs.send(tea.WindowSizeMsg{Width: w, Height: h}, nil)
	hs.run(m.Init(), nil)
	return hs
}

// spawn starts cmd, unwrapping batches and sequences.
func (hs *harness) spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if msg := cmd(); msg != nil {
			hs.out <- msg
		}
	}()
}

// run feeds results of cmd (and of whatever they start) back into the model
// until done reports true for a delivered message, or nothing arrives for
// a quiet spell when done is nil.
func (hs *harness) run(cmd tea.Cmd, done func(tea.Msg) bool) {
	hs.spawn(cmd)
	quiet := 150 * time.Millisecond
	for {
		var msg tea.Msg
		if done != nil {
			msg = <-hs.out
		} else {
			select {
			case msg = <-hs.out:
			case <-time.After(quiet):
				return
			}
		}
		if cmds, ok := children(msg); ok {
			for _, c := range cmds {
				hs.spawn(c)
			}
			continue
		}
		hs.send(msg, nil)
		if done != nil && done(msg) {
			return
		}
	}
}

func (hs *harness) send(msg tea.Msg, done func(tea.Msg) bool) {
	var cmd tea.Cmd
	hs.m, cmd = hs.m.Update(msg)
	if done != nil {
		hs.run(cmd, done)
		return
	}
	hs.spawn(cmd)
}

// children recognises tea.BatchMsg and Bubble Tea's unexported sequenceMsg.
func children(msg tea.Msg) ([]tea.Cmd, bool) {
	if b, ok := msg.(tea.BatchMsg); ok {
		return b, true
	}
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		cmds := make([]tea.Cmd, v.Len())
		for i := range cmds {
			cmds[i], _ = v.Index(i).Interface().(tea.Cmd)
		}
		return cmds, true
	}
	return nil, false
}

func keyMsg(k string) tea.KeyMsg {
	if k == "backspace" {
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func isChanged(msg tea.Msg) bool { _, ok := msg.(filter.ChangedMsg); return ok }

// machinesFilter opens the Machines page with the filter bar focused.
func machinesFilter(tb testing.TB, w, h int) *harness {
	tb.Helper()
	hs := newHarness(tb, w, h)
	for _, k := range []string{"1", "m"} {
		hs.send(keyMsg(k), nil)
		hs.run(nil, nil)
	}
	hs.send(keyMsg("/"), nil)
	hs.run(nil, nil)
	if v := uitest.MaxWidth(hs.m.View()); v == 0 {
		tb.Fatal("app rendered nothing")
	}
	if s := hs.m.View(); !strings.Contains(s, fmt.Sprint(perfNodes)) {
		tb.Fatalf("machines page does not show %d machines:\n%s", perfNodes, s)
	}
	return hs
}

// typing is the keystroke cycle: type a query a character at a time, then
// delete it, so every step changes the filter.
var typing = []string{"w", "e", "b", "backspace", "backspace", "backspace", "t", "a", "g", ":", "backspace", "backspace", "backspace", "backspace"}

// frame types one key and renders the next frame: the key, the filter change
// it causes, the rebuild of the 2,000-row list and the full View.
func (hs *harness) frame(k string) string {
	hs.send(keyMsg(k), isChanged)
	return hs.m.View()
}

// BenchmarkFilterKeystroke2000 measures one filter keystroke to rendered
// frame on the Machines page with 2,000 machines (T170). The budget is 16ms.
func BenchmarkFilterKeystroke2000(b *testing.B) {
	for _, sz := range [][2]int{{120, 40}, {200, 60}} {
		b.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(b *testing.B) {
			hs := machinesFilter(b, sz[0], sz[1])
			b.ReportAllocs()
			b.ResetTimer()
			i := 0
			for b.Loop() {
				hs.frame(typing[i%len(typing)])
				i++
			}
		})
	}
}

// BenchmarkSetRowsView2000 measures the table alone: replace 2,000 rows and
// render a viewport.
func BenchmarkSetRowsView2000(b *testing.B) {
	rows := make([]table.Row, perfNodes)
	for i := range rows {
		rows[i] = plainRow{key: fmt.Sprint(i), name: fmt.Sprintf("node-%04d", (i*7919)%perfNodes)}
	}
	tb := table.New(uitest.StyleFor("tokyo-night"), icons.Unicode, []table.Column{
		{Key: "name", Title: "NAME", Width: 20, Flex: true},
		{Key: "user", Title: "USER", Width: 10},
	})
	tb.SetSort("name", false)
	tb.SetSize(120, 38)
	b.ReportAllocs()
	for b.Loop() {
		tb.SetRows(rows)
		_ = tb.View()
	}
}

// TestFilterKeystrokeBudget keeps the T170 budget from regressing: the median
// keystroke-to-frame time with 2,000 machines must stay under 16ms. The
// measured time is a fraction of that, so slow CI runners still pass; it is
// skipped under -short and -race, where timings mean nothing.
func TestFilterKeystrokeBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing test: skipped under -short and -race")
	}
	hs := machinesFilter(t, 120, 40)
	for _, k := range typing { // warm up
		hs.frame(k)
	}
	var times []time.Duration
	for range 5 {
		for _, k := range typing {
			start := time.Now()
			hs.frame(k)
			times = append(times, time.Since(start))
		}
	}
	slices.Sort(times)
	med := times[len(times)/2]
	t.Logf("filter keystroke to frame, %d machines: median %v, p90 %v, max %v",
		perfNodes, med, times[len(times)*9/10], times[len(times)-1])
	if med > frameBudget {
		t.Errorf("median keystroke-to-frame %v exceeds the %v budget", med, frameBudget)
	}
}

type plainRow struct{ key, name string }

func (r plainRow) Key() string          { return r.key }
func (r plainRow) Cell(c string) string { return r.SortValue(c) }
func (r plainRow) SortValue(c string) string {
	if c == "name" {
		return r.name
	}
	return "user"
}
