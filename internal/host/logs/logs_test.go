package logs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/fathom/internal/host"
)

func TestParseLine(t *testing.T) {
	ts := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		in     string
		level  host.LogLevel
		msg    string
		time   time.Time
		fields map[string]string
		refs   []uint64
	}{
		{name: "console", in: "2026-10-28T12:00:00Z INF Node connected node.id=12 node.name=laptop",
			level: host.LevelInfo, msg: "Node connected", time: ts, fields: map[string]string{"node.id": "12", "node.name": "laptop"}, refs: []uint64{12}},
		{name: "console ansi quoted caller", in: "\x1b[90m2026-10-28T12:00:00Z\x1b[0m \x1b[31mERR\x1b[0m hscontrol/poll.go:88 > failed to send map response error=\"broken pipe\" node=7",
			level: host.LevelError, msg: "failed to send map response", time: ts, fields: map[string]string{"error": "broken pipe", "node": "7", "caller": "hscontrol/poll.go:88"}, refs: []uint64{7}},
		{name: "console no fields", in: "2026-10-28T12:00:00Z WRN No TLS configured", level: host.LevelWarn, msg: "No TLS configured", time: ts},
		{name: "zerolog json", in: `{"level":"debug","node":{"id":3,"name":"nas"},"machine":"5","time":"2026-10-28T12:00:00Z","message":"map request"}`,
			level: host.LevelDebug, msg: "map request", time: ts, fields: map[string]string{"node.id": "3", "node.name": "nas", "machine": "5"}, refs: []uint64{3, 5}},
		{name: "journald wrapping console", in: `{"__REALTIME_TIMESTAMP":"1793188800000000","PRIORITY":"6","MESSAGE":"2026-10-28T12:00:00Z ERR policy reload failed error=\"bad acl\""}`,
			level: host.LevelError, msg: "policy reload failed", time: ts, fields: map[string]string{"error": "bad acl"}},
		{name: "journald plain message uses priority", in: `{"__REALTIME_TIMESTAMP":"1793188800000000","PRIORITY":"4","MESSAGE":"Started headscale coordination server."}`,
			level: host.LevelWarn, msg: "Started headscale coordination server.", time: time.UnixMicro(1793188800000000).UTC()},
		{name: "journald wrapping json", in: `{"__REALTIME_TIMESTAMP":"1793188800000000","MESSAGE":"{\"level\":\"warn\",\"id\":\"9\",\"message\":\"node expired\"}"}`,
			level: host.LevelWarn, msg: "node expired", time: time.UnixMicro(1793188800000000).UTC(), fields: map[string]string{"id": "9"}, refs: []uint64{9}},
		{name: "journald byte array", in: `{"MESSAGE":[104,105]}`, level: host.LevelInfo, msg: "hi"},
		{name: "garbage", in: "}{ not a log", level: host.LevelInfo, msg: "}{ not a log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ParseLine(tt.in)
			if e.Level != tt.level || e.Msg != tt.msg {
				t.Fatalf("got level %v msg %q", e.Level, e.Msg)
			}
			if !tt.time.IsZero() && !e.Time.Equal(tt.time) {
				t.Errorf("time %v want %v", e.Time, tt.time)
			}
			for k, v := range tt.fields {
				if e.Fields[k] != v {
					t.Errorf("field %s = %q want %q (all %v)", k, e.Fields[k], v, e.Fields)
				}
			}
			slices.Sort(e.NodeRefs)
			if !slices.Equal(e.NodeRefs, tt.refs) {
				t.Errorf("refs %v want %v", e.NodeRefs, tt.refs)
			}
			if e.Raw != tt.in {
				t.Error("raw not kept")
			}
		})
	}
}

func FuzzParseLine(f *testing.F) {
	for _, s := range []string{
		"2026-10-28T12:00:00Z INF msg a=b",
		`{"level":"info","message":"x","node":{"id":1}}`,
		`{"MESSAGE":"2026-10-28T12:00:00Z DBG y node=2","__REALTIME_TIMESTAMP":"1"}`,
		"\x1b[31mERR\x1b[0m z k=\"v\\\"",
		`{"MESSAGE":{"a":1}}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e := ParseLine(s)
		if e.Fields == nil {
			t.Fatal("nil fields")
		}
		if e.Level < host.LevelTrace || e.Level > host.LevelFatal {
			t.Fatal("level out of range")
		}
	})
}

type fakeStream struct {
	name string
	args []string
	data string
}

func (f *fakeStream) start(ctx context.Context, name string, args ...string) (io.ReadCloser, error) {
	f.name, f.args = name, args
	return io.NopCloser(strings.NewReader(f.data)), nil
}

func collect(t *testing.T, ch <-chan host.LogEntry) []host.LogEntry {
	t.Helper()
	var out []host.LogEntry
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

func TestCommandSources(t *testing.T) {
	since := time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC)
	data := `{"MESSAGE":"2026-10-28T12:00:00Z INF one node.id=1"}` + "\n\n" + `{"MESSAGE":"2026-10-28T12:00:01Z WRN two"}` + "\n"
	tests := []struct {
		name string
		src  func(StreamFunc) Source
		q    host.LogQuery
		want string
	}{
		{"journald", func(s StreamFunc) Source { return Journald{Start: s} }, host.LogQuery{Follow: true, Tail: 50},
			"journalctl -u headscale -o json -n 50 -f"},
		{"journald since", func(s StreamFunc) Source { return Journald{Start: s, Unit: "headscale.service"} }, host.LogQuery{Since: since},
			"journalctl -u headscale.service -o json -n 500 --since @1793185200"},
		{"docker", func(s StreamFunc) Source { return Container{Start: s, Name: "headscale"} }, host.LogQuery{Follow: true, Tail: 10},
			"docker logs --tail 10 -f headscale"},
		{"podman since", func(s StreamFunc) Source { return Container{Start: s, Runtime: "podman", Name: "hs"} }, host.LogQuery{Since: since},
			"podman logs --tail 500 --since 2026-10-28T11:00:00Z hs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &fakeStream{data: data}
			ch, err := tt.src(fs.start).Stream(context.Background(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			got := collect(t, ch)
			if cmd := fs.name + " " + strings.Join(fs.args, " "); cmd != tt.want {
				t.Errorf("ran %q want %q", cmd, tt.want)
			}
			if len(got) != 2 || got[0].Msg != "one" || got[1].Level != host.LevelWarn {
				t.Fatalf("entries %+v", got)
			}
		})
	}
}

func TestCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := Journald{Start: func(context.Context, string, ...string) (io.ReadCloser, error) { return pr, nil }}.Stream(ctx, host.LogQuery{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	go pw.Write([]byte("2026-10-28T12:00:00Z INF hello\n"))
	if e := <-ch; e.Msg != "hello" {
		t.Fatalf("got %q", e.Msg)
	}
	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(3 * time.Second):
		t.Fatal("channel not closed after cancel")
	}
}

func TestFileTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "headscale.log")
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString("2026-10-28T12:00:00Z INF line" + string(rune('a'+i-1)) + "\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ch, err := File{Path: p}.Stream(context.Background(), host.LogQuery{Tail: 3})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, ch)
	if len(got) != 3 || got[0].Msg != "liner" || got[2].Msg != "linet" {
		t.Fatalf("tail = %+v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err = File{Path: p, Poll: 10 * time.Millisecond}.Stream(ctx, host.LogQuery{Tail: 1, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if e := <-ch; e.Msg != "linet" {
		t.Fatalf("first = %q", e.Msg)
	}
	fh, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	fh.WriteString("2026-10-28T12:00:05Z ERR appended node=4\n")
	fh.Close()
	select {
	case e := <-ch:
		if e.Msg != "appended" || e.Level != host.LevelError || !slices.Equal(e.NodeRefs, []uint64{4}) {
			t.Fatalf("appended = %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("follow did not pick up appended line")
	}
	cancel()
}
