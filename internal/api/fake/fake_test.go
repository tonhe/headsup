package fake

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/apitest"
)

func TestContract(t *testing.T) {
	for _, v := range []string{"v0.30.0", "v0.29.6", "v0.29.0"} {
		t.Run(v, func(t *testing.T) {
			apitest.RunContract(t, func(t *testing.T) api.Backend { return Demo(WithVersion(v)) })
		})
	}
}

func TestSeedCopyInSync(t *testing.T) {
	disk, err := os.ReadFile("../../../testdata/seed/demo.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(disk, demoSeed) {
		t.Error("testdata/seed/demo.json differs from internal/api/fake/seed.json")
	}
}

func TestFailNextAndCalls(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		opt    bool
		method string
		kind   api.ErrorKind
	}{
		{"option", true, "ListNodes", api.KindUnreachable},
		{"method", false, "ListNodes", api.KindUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b *Backend
			if tt.opt {
				b = Demo(FailNext(tt.method, tt.kind))
			} else {
				b = Demo()
				b.FailNext(tt.method, tt.kind)
			}
			_, err := b.ListNodes(ctx, api.NodeFilter{User: "bob"})
			if !api.IsKind(err, tt.kind) {
				t.Fatalf("got %v, want kind %s", err, tt.kind)
			}
			if _, err := b.ListNodes(ctx, api.NodeFilter{}); err != nil {
				t.Fatalf("second call failed: %v", err)
			}
			calls := b.Calls()
			if len(calls) != 2 || calls[0].Method != "ListNodes" || calls[0].Args[0].(api.NodeFilter).User != "bob" {
				t.Errorf("calls = %+v", calls)
			}
		})
	}
}

func TestLatencyHonoursContext(t *testing.T) {
	b := Demo(WithLatency(time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := b.ListUsers(ctx, api.UserFilter{})
	if !api.IsKind(err, api.KindUnreachable) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("latency ignored ctx")
	}
}

func TestCheckPolicyPosition(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantLine int
		ok       bool
	}{
		{"valid with comments", "// c\n{\"acls\": [],}\n", 0, true},
		{"bad token", "{\n  \"acls\": [\n    x\n  ]\n}", 3, false},
		{"not object", "[]", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Demo().CheckPolicy(context.Background(), tt.raw)
			if tt.ok {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				return
			}
			e := api.AsError(err)
			if e == nil || e.Kind != api.KindInvalid || e.Line != tt.wantLine || e.Col == 0 {
				t.Fatalf("got %+v, want Invalid at line %d", e, tt.wantLine)
			}
		})
	}
}

func TestConcurrent(t *testing.T) {
	b := Demo()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.ListNodes(ctx, api.NodeFilter{})
			_, _ = b.SetTags(ctx, uint64(i%5+1), []string{"tag:ci"})
		}()
	}
	wg.Wait()
	if got := len(b.Calls()); got != 40 {
		t.Errorf("calls = %d, want 40", got)
	}
}
