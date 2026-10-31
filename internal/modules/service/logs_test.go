package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func entry(i int, lvl host.LogLevel) host.LogEntry {
	return host.LogEntry{Level: lvl, Msg: fmt.Sprintf("line %d", i), Fields: map[string]string{"n": fmt.Sprint(i)}}
}

func TestRingWraps(t *testing.T) {
	r := NewRing(3)
	for i := range 5 {
		if seq := r.Push(entry(i, host.LevelInfo)); seq != uint64(i) {
			t.Fatalf("seq %d for push %d", seq, i)
		}
	}
	if r.Len() != 3 || r.First() != 2 || r.Cap() != 3 {
		t.Fatalf("len %d first %d", r.Len(), r.First())
	}
	if _, ok := r.At(1); ok {
		t.Error("seq 1 should have been dropped")
	}
	if e, ok := r.At(4); !ok || e.Msg != "line 4" {
		t.Errorf("At(4) = %+v %v", e, ok)
	}
	if _, ok := r.At(5); ok {
		t.Error("seq 5 not pushed yet")
	}
}

func TestViewerKeepsTenThousand(t *testing.T) {
	v := NewViewer(RingSize)
	batch := make([]host.LogEntry, 0, 1000)
	for i := range 12_000 {
		lvl := host.LevelInfo
		if i%100 == 0 {
			lvl = host.LevelError
		}
		batch = append(batch, entry(i, lvl))
		if len(batch) == cap(batch) {
			v.Append(batch)
			batch = batch[:0]
		}
	}
	if v.Total() != RingSize || v.Len() != RingSize {
		t.Fatalf("total %d len %d", v.Total(), v.Len())
	}
	if e, _ := v.Cursor(); e.Msg != "line 11999" {
		t.Errorf("following cursor on %q", e.Msg)
	}
	// Only the window is materialised.
	es, cur := v.Window(20)
	if len(es) != 20 || cur != 19 || es[0].Msg != "line 11980" {
		t.Errorf("window: %d lines, cursor %d, first %q", len(es), cur, es[0].Msg)
	}
	v.SetMinLevel(host.LevelError)
	if v.Len() != 100 {
		t.Errorf("errors among the last 10k: %d", v.Len())
	}
	// Filtered entries that fall out of the ring are trimmed.
	v.Append([]host.LogEntry{entry(12_000, host.LevelError)})
	if v.Len() != 100 {
		t.Errorf("after one more error (and one dropped): %d", v.Len())
	}
}

func TestViewerFilterKeepsCursor(t *testing.T) {
	v := NewViewer(100)
	for i := range 50 {
		lvl := host.LevelDebug
		if i%10 == 5 {
			lvl = host.LevelWarn
		}
		v.Append([]host.LogEntry{entry(i, lvl)})
	}
	v.Home()
	v.Move(25) // line 25, a warning
	if e, _ := v.Cursor(); e.Msg != "line 25" {
		t.Fatalf("cursor %q", e.Msg)
	}
	v.SetMinLevel(host.LevelWarn)
	if e, _ := v.Cursor(); e.Msg != "line 25" {
		t.Errorf("cursor after filter %q", e.Msg)
	}
	v.SetMinLevel(host.LevelTrace)
	v.Move(1)
	if e, _ := v.Cursor(); e.Msg != "line 26" {
		t.Errorf("cursor after unfilter+1 %q", e.Msg)
	}
	// Cycling wraps.
	for range len(filterLevels) {
		v.CycleLevel()
	}
	if v.MinLevel() != host.LevelTrace {
		t.Errorf("cycle should wrap, got %v", v.MinLevel())
	}
	v.SetSearch(`line 4[0-2]\b`)
	if v.Len() != 3 || v.SearchErr() != nil {
		t.Errorf("regex search: %d %v", v.Len(), v.SearchErr())
	}
	v.SetSearch("[")
	if v.SearchErr() == nil || v.Len() != 0 {
		t.Errorf("invalid regex should fall back to literal: %d %v", v.Len(), v.SearchErr())
	}
	v.SetSearch("N=4") // fields are searchable, case-insensitively
	if v.Len() != 11 {
		t.Errorf("field search: %d", v.Len())
	}
	v.SetSearch("")
	if v.Len() != 50 || v.Search() != "" {
		t.Errorf("clear: %d", v.Len())
	}
}

func TestViewerNextErrorWraps(t *testing.T) {
	v := NewViewer(100)
	if v.NextError(1) {
		t.Error("empty viewer has no errors")
	}
	for i := range 10 {
		lvl := host.LevelInfo
		if i == 2 || i == 7 {
			lvl = host.LevelError
		}
		v.Append([]host.LogEntry{entry(i, lvl)})
	}
	want := []string{"line 7", "line 2", "line 7"}
	for _, w := range want {
		if !v.NextError(1) {
			t.Fatal("no error found")
		}
		if e, _ := v.Cursor(); e.Msg != w {
			t.Errorf("next error %q, want %q", e.Msg, w)
		}
	}
	if v.Following() {
		t.Error("jumping releases follow")
	}
	v.NextError(-1)
	if e, _ := v.Cursor(); e.Msg != "line 2" {
		t.Errorf("previous error %q", e.Msg)
	}
	v.SetMinLevel(host.LevelFatal)
	if v.NextError(1) {
		t.Error("no lines shown, nothing to jump to")
	}
}

func TestViewerPauseHoldsLines(t *testing.T) {
	v := NewViewer(5)
	v.SetPaused(true)
	v.SetPaused(true) // idempotent
	for i := range 8 {
		v.Append([]host.LogEntry{entry(i, host.LevelInfo)})
	}
	if v.Total() != 0 || v.Held() != 5 {
		t.Fatalf("paused: total %d held %d", v.Total(), v.Held())
	}
	v.SetPaused(false)
	if v.Total() != 5 || v.Held() != 0 {
		t.Fatalf("resumed: total %d held %d", v.Total(), v.Held())
	}
	if e, _ := v.Cursor(); e.Msg != "line 7" {
		t.Errorf("resume while following lands on newest, got %q", e.Msg)
	}
	if es, cur := v.Window(0); es != nil || cur != -1 {
		t.Error("zero-height window")
	}
	v.ToggleExpanded()
	if !v.Expanded() {
		t.Error("expand")
	}
}

func TestNodeRefs(t *testing.T) {
	st := store.New()
	st.Nodes = []api.Node{
		{ID: 3, Name: "laptop", IPAddresses: []netip.Addr{netip.MustParseAddr("100.64.0.3")}},
		{ID: 9, Name: "pi", GivenName: "garage-pi"},
	}
	e := host.LogEntry{Msg: "peer garage-pi changed", NodeRefs: []uint64{3},
		Fields: map[string]string{"addr": "100.64.0.3:41641", "other": "x"}}
	got := nodeRefs(e, st)
	if fmt.Sprint(got) != "[3 9]" {
		t.Errorf("refs %v", got)
	}
	if got := nodeRefs(host.LogEntry{NodeRefs: []uint64{4}}, nil); fmt.Sprint(got) != "[4]" {
		t.Errorf("no store: %v", got)
	}
	s := uitest.StyleFor("")
	pairs := detailPairs(s, e, st, time.UTC)
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString(p.Key + "=" + ansi.Strip(p.Value) + "\n")
	}
	for _, want := range []string{"Time=-", "Machine=laptop (#3", "Machine=garage-pi (#9", "addr=100.64.0.3:41641"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("detail missing %q:\n%s", want, b.String())
		}
	}
}

func TestRenderLine(t *testing.T) {
	s := uitest.StyleFor("")
	e := host.LogEntry{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Level: host.LevelWarn, Msg: "hello",
		Fields: map[string]string{"b": "two words", "a": "1", "c": ""}}
	got := ansi.Strip(renderLine(s, icons.Unicode, e, time.UTC, 80, false))
	if got != `03:04:05 WRN hello a=1 b="two words" c=""` {
		t.Errorf("line %q", got)
	}
	cur := ansi.Strip(renderLine(s, icons.Unicode, e, time.UTC, 20, true))
	if !strings.HasSuffix(cur, "…") || len([]rune(cur)) != 20 {
		t.Errorf("cursor line %q", cur)
	}
	if stamp(time.Time{}, time.UTC) != "--:--:--" {
		t.Error("zero stamp")
	}
}

func TestMemory(t *testing.T) {
	for in, want := range map[uint64]string{0: "-", 512 << 10: "512 KiB", 48 << 20: "48.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := memory(in); got != want {
			t.Errorf("memory(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestErrors(t *testing.T) {
	base := errors.New("exec: headscale not found")
	cases := []struct {
		err  error
		want string
	}{
		{&RefusedError{Op: "restart", Err: base}, "restart refused: the config check could not run: exec: headscale not found"},
		{&RefusedError{Op: "start", Issues: []host.Issue{{Severity: host.SevWarn, Message: "w"}, {Severity: host.SevError, Message: "bad", Line: 4}}}, "start refused: config check failed: line 4: bad"},
		{&RefusedError{Op: "start", Issues: []host.Issue{{Severity: host.SevError, Message: "line 9: bad"}}}, "start refused: config check failed: line 9: bad"},
		{&RefusedError{Op: "start"}, "start refused: config check failed"},
		{&UnhealthyError{Op: "start", State: "failed"}, "headscale is not healthy after start (state failed); check the logs below"},
	}
	for _, c := range cases {
		if c.err.Error() != c.want {
			t.Errorf("got %q, want %q", c.err.Error(), c.want)
		}
	}
	if !errors.Is(&RefusedError{Err: base}, base) {
		t.Error("RefusedError unwraps")
	}
}

// slowService never becomes healthy, for the health timeout.
type slowService struct{ host.ServiceManager }

func (slowService) Status(context.Context) (host.ServiceStatus, error) {
	return host.ServiceStatus{State: "activating"}, nil
}

func TestWaitHealthyTimesOut(t *testing.T) {
	defer func(t, e time.Duration) { healthTimeout, healthEvery = t, e }(healthTimeout, healthEvery)
	healthTimeout, healthEvery = 30*time.Millisecond, 5*time.Millisecond
	_, err := waitHealthy(context.Background(), slowService{}, "restart")
	var ue *UnhealthyError
	if !errors.As(err, &ue) || ue.State != "activating" {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	healthTimeout = time.Hour
	if _, err := waitHealthy(ctx, slowService{}, "restart"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestReadBatchClosed(t *testing.T) {
	ch := make(chan host.LogEntry, 3)
	ch <- entry(1, host.LevelInfo)
	ch <- entry(2, host.LevelInfo)
	close(ch)
	msg := readBatch(7, ch)().(logMsg)
	if msg.id != 7 || len(msg.entries) != 2 || !msg.closed {
		t.Errorf("batch %+v", msg)
	}
	msg = readBatch(7, ch)().(logMsg)
	if !msg.closed || len(msg.entries) != 0 {
		t.Errorf("empty closed %+v", msg)
	}
}
