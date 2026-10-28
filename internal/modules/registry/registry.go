// Package registry collects modules. Each module registers a constructor from
// its init(); internal/modules/all blank-imports every built-in module, so a
// new module costs one directory plus one import line.
package registry

import (
	"slices"
	"sort"
	"sync"

	"github.com/tonhe/fathom/internal/module"
)

var (
	mu    sync.Mutex
	ctors []func() module.Module
)

// Register adds a module constructor. Call it from init().
func Register(ctor func() module.Module) {
	mu.Lock()
	defer mu.Unlock()
	ctors = append(ctors, ctor)
}

// All builds every registered module not listed in disabled, sorted by Order
// then ID. Each call returns fresh instances.
func All(disabled []string) []module.Module {
	mu.Lock()
	cs := append([]func() module.Module(nil), ctors...)
	mu.Unlock()

	var out []module.Module
	for _, c := range cs {
		m := c()
		if slices.Contains(disabled, m.ID()) {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order() != out[j].Order() {
			return out[i].Order() < out[j].Order()
		}
		return out[i].ID() < out[j].ID()
	})
	return out
}

// Reset clears registrations (tests only).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	ctors = nil
}
