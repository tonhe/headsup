package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/fathom/internal/store"
	"testing"
	"time"

	"github.com/tonhe/fathom/internal/api/fake"
	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/modules/dashboard"
	"github.com/tonhe/fathom/internal/modules/machines"
	"github.com/tonhe/fathom/internal/modules/pending"
)

// TestStartupBudget checks SC-009: with realistic local latency (each API call
// ~60ms, as through the CLI or socket, plus host enrichment) the first full
// dashboard frame renders in under 3 seconds.
func TestStartupBudget(t *testing.T) {
	b := fake.Demo(fake.WithLatency(60 * time.Millisecond))
	h := demoHostFor(t, fake.Demo())
	start := time.Now()
	m := New(Options{
		Backend: b, Host: h, Refresh: time.Hour,
		Modules: []module.Module{dashboard.New(), machines.New(), pending.New()},
	})
	// Run commands concurrently, as Bubble Tea does, feeding results back in
	// arrival order until the dashboard has data or the budget is spent.
	msgs := make(chan tea.Msg, 64)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, bc := range batch {
					run(bc)
				}
				return
			}
			msgs <- msg
		}()
	}
	m.Update(windowSize(120, 40))
	run(m.Init())
	deadline := time.After(3 * time.Second)
	for !strings.Contains(m.View(), "42 / 51") {
		select {
		case msg := <-msgs:
			if msg != nil {
				_, next := m.Update(msg)
				run(next)
			}
		case <-deadline:
			t.Fatalf("dashboard not loaded within 3s:\n%s", m.View())
		}
	}
	elapsed := time.Since(start)
	if m.store.Loaded&store.SliceNodes == 0 || len(m.store.Extras) == 0 {
		t.Error("nodes and host enrichment should both be loaded")
	}
	t.Logf("first full frame in %v", elapsed.Round(time.Millisecond))
}
