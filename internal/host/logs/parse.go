// Package logs streams headscale's logs from journald, a container runtime
// or a file, and parses each line (journald JSON, zerolog JSON, zerolog
// console text) into a host.LogEntry.
package logs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/fathom/internal/host"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// ParseLine parses one raw log line. It never fails: unrecognised input
// becomes an Info entry whose Msg is the line itself.
func ParseLine(s string) host.LogEntry {
	e, _ := parse(s)
	return e
}

func parse(s string) (host.LogEntry, bool) {
	raw := strings.TrimRight(s, "\r\n")
	e := host.LogEntry{Raw: raw, Level: host.LevelInfo, Fields: map[string]string{}}
	t := strings.TrimSpace(ansiRE.ReplaceAllString(raw, ""))
	if strings.HasPrefix(t, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			if msg, ok := m["MESSAGE"]; ok {
				inner, hasLevel := parse(journalMessage(msg))
				inner.Raw = raw
				if inner.Time.IsZero() {
					inner.Time = journalTime(m)
				}
				if p, ok := m["PRIORITY"]; ok && !hasLevel {
					inner.Level = priorityLevel(p)
				}
				return inner, true
			}
			_, hasLevel := m["level"]
			parseZerologJSON(&e, m)
			return finish(e), hasLevel
		}
	}
	hasLevel := parseConsole(&e, t)
	return finish(e), hasLevel
}

func finish(e host.LogEntry) host.LogEntry {
	e.NodeRefs = nodeRefs(e.Fields)
	return e
}

func journalMessage(v any) string {
	switch m := v.(type) {
	case string:
		return m
	case []any: // journald encodes non-UTF-8 messages as byte arrays
		b := make([]byte, 0, len(m))
		for _, x := range m {
			if f, ok := x.(float64); ok && f >= 0 && f < 256 {
				b = append(b, byte(f))
			}
		}
		return string(b)
	}
	return fmt.Sprint(v)
}

func journalTime(m map[string]any) time.Time {
	s, _ := m["__REALTIME_TIMESTAMP"].(string)
	us, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(us).UTC()
}

func priorityLevel(v any) host.LogLevel {
	s := fmt.Sprint(v)
	switch s {
	case "0", "1", "2":
		return host.LevelFatal
	case "3":
		return host.LevelError
	case "4":
		return host.LevelWarn
	case "7":
		return host.LevelDebug
	}
	return host.LevelInfo
}

func parseZerologJSON(e *host.LogEntry, m map[string]any) {
	for k, v := range m {
		switch k {
		case "level":
			e.Level = levelOf(fmt.Sprint(v))
		case "time":
			e.Time = parseTime(v)
		case "message":
			e.Msg = fmt.Sprint(v)
		default:
			flatten(e.Fields, k, v)
		}
	}
}

func flatten(dst map[string]string, prefix string, v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			flatten(dst, prefix+"."+k, vv)
		}
	case string:
		dst[prefix] = x
	case float64:
		dst[prefix] = strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		dst[prefix] = ""
	default:
		b, _ := json.Marshal(x)
		dst[prefix] = string(b)
	}
}

func parseTime(v any) time.Time {
	switch x := v.(type) {
	case string:
		for _, l := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(l, x); err == nil {
				return t
			}
		}
	case float64:
		sec := int64(x)
		return time.Unix(sec, int64((x-float64(sec))*1e9)).UTC()
	}
	return time.Time{}
}

var levelTokens = map[string]host.LogLevel{
	"TRC": host.LevelTrace, "DBG": host.LevelDebug, "INF": host.LevelInfo,
	"WRN": host.LevelWarn, "ERR": host.LevelError, "FTL": host.LevelFatal, "PNC": host.LevelFatal,
}

func levelOf(s string) host.LogLevel {
	switch strings.ToLower(s) {
	case "trace", "trc":
		return host.LevelTrace
	case "debug", "dbg":
		return host.LevelDebug
	case "warn", "warning", "wrn":
		return host.LevelWarn
	case "error", "err":
		return host.LevelError
	case "fatal", "panic", "ftl", "pnc":
		return host.LevelFatal
	}
	return host.LevelInfo
}

var fieldRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*=`)

// parseConsole parses zerolog's console format:
// 2026-10-28T12:00:00Z INF [caller.go:12 >] message key=value key="quoted value"
func parseConsole(e *host.LogEntry, t string) bool {
	hasLevel := false
	rest := t
	if tok, after, ok := strings.Cut(rest, " "); ok {
		if ts := parseTime(tok); !ts.IsZero() {
			e.Time = ts
			rest = strings.TrimLeft(after, " ")
		}
	}
	if tok, after, ok := strings.Cut(rest+" ", " "); ok {
		if lvl, ok := levelTokens[tok]; ok {
			e.Level = lvl
			hasLevel = true
			rest = strings.TrimSpace(after)
		}
	}
	// zerolog caller: "file.go:123 >"
	if i := strings.Index(rest, " > "); i > 0 && !strings.Contains(rest[:i], " ") && strings.Contains(rest[:i], ":") {
		e.Fields["caller"] = rest[:i]
		rest = rest[i+3:]
	}
	toks := splitTokens(rest)
	var msg []string
	i := 0
	for ; i < len(toks); i++ {
		if fieldRE.MatchString(toks[i]) {
			break
		}
		msg = append(msg, toks[i])
	}
	for ; i < len(toks); i++ {
		k, v, ok := strings.Cut(toks[i], "=")
		if !ok || !fieldRE.MatchString(toks[i]) {
			continue
		}
		if strings.HasPrefix(v, `"`) {
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
		}
		e.Fields[k] = v
	}
	e.Msg = strings.Join(msg, " ")
	return hasLevel
}

// splitTokens splits on spaces, keeping double-quoted values together.
func splitTokens(s string) []string {
	var out []string
	var b strings.Builder
	inQ, esc := false, false
	for _, r := range s {
		switch {
		case esc:
			esc = false
		case r == '\\' && inQ:
			esc = true
		case r == '"':
			inQ = !inQ
		case r == ' ' && !inQ:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

var nodeKeys = []string{"node.id", "node_id", "nodeid", "node", "machine", "machine.id", "machine_id", "id", "node.ID", "src_node", "dst_node", "peer.id"}

// nodeRefs extracts node IDs from well-known field names.
func nodeRefs(f map[string]string) []uint64 {
	var out []uint64
	for _, k := range nodeKeys {
		v, ok := f[k]
		if !ok {
			continue
		}
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '[' || r == ']' }) {
			part = strings.TrimPrefix(strings.TrimPrefix(part, "node:"), "#")
			if n, err := strconv.ParseUint(part, 10, 64); err == nil && n > 0 && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}
