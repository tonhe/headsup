package logs

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

// StreamFunc starts a command and returns its combined stdout+stderr. The
// process must be killed when ctx is cancelled; Close waits for it.
type StreamFunc func(ctx context.Context, name string, args ...string) (io.ReadCloser, error)

// Source produces log entries.
type Source interface {
	Stream(ctx context.Context, q host.LogQuery) (<-chan host.LogEntry, error)
}

// DefaultTail is the number of lines read when LogQuery.Tail is 0.
const DefaultTail = 500

func tail(q host.LogQuery) int {
	if q.Tail > 0 {
		return q.Tail
	}
	return DefaultTail
}

// ExecStream is the real StreamFunc.
func ExecStream(ctx context.Context, name string, args ...string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, err
	}
	go func() { pw.CloseWithError(cmd.Wait()) }()
	return &procReader{PipeReader: pr}, nil
}

type procReader struct{ *io.PipeReader }

func (p *procReader) Read(b []byte) (int, error) {
	n, err := p.PipeReader.Read(b)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = io.EOF
	}
	return n, err
}

// pump scans r into a channel of parsed entries until EOF or ctx is done.
func pump(ctx context.Context, r io.ReadCloser) <-chan host.LogEntry {
	ch := make(chan host.LogEntry, 64)
	go func() {
		defer close(ch)
		defer r.Close()
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				r.Close()
			case <-done:
			}
		}()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if sc.Text() == "" {
				continue
			}
			select {
			case ch <- ParseLine(sc.Text()):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// Journald reads `journalctl -u <unit> -o json`.
type Journald struct {
	Start StreamFunc
	Unit  string
}

func (j Journald) Args(q host.LogQuery) []string {
	unit := j.Unit
	if unit == "" {
		unit = "headscale"
	}
	args := []string{"-u", unit, "-o", "json", "-n", strconv.Itoa(tail(q))}
	if q.Follow {
		args = append(args, "-f")
	}
	if !q.Since.IsZero() {
		args = append(args, "--since", "@"+strconv.FormatInt(q.Since.Unix(), 10))
	}
	return args
}

func (j Journald) Stream(ctx context.Context, q host.LogQuery) (<-chan host.LogEntry, error) {
	r, err := j.Start(ctx, "journalctl", j.Args(q)...)
	if err != nil {
		return nil, err
	}
	return pump(ctx, r), nil
}

// Container reads `<runtime> logs`.
type Container struct {
	Start   StreamFunc
	Runtime string
	Name    string
}

func (c Container) Args(q host.LogQuery) []string {
	args := []string{"logs", "--tail", strconv.Itoa(tail(q))}
	if q.Follow {
		args = append(args, "-f")
	}
	if !q.Since.IsZero() {
		args = append(args, "--since", q.Since.UTC().Format(time.RFC3339))
	}
	return append(args, c.Name)
}

func (c Container) Stream(ctx context.Context, q host.LogQuery) (<-chan host.LogEntry, error) {
	rt := c.Runtime
	if rt == "" {
		rt = "docker"
	}
	r, err := c.Start(ctx, rt, c.Args(q)...)
	if err != nil {
		return nil, err
	}
	return pump(ctx, r), nil
}

// File tails a log file, polling for appended data when following.
type File struct {
	Path string
	Poll time.Duration // default 250ms
}

func (f File) Stream(ctx context.Context, q host.LogQuery) (<-chan host.LogEntry, error) {
	fh, err := os.Open(f.Path)
	if err != nil {
		if os.IsPermission(err) {
			return nil, &host.ErrPermission{Need: host.NeedLogs, Op: "read " + f.Path, FixCommand: "sudo chmod o+r " + f.Path}
		}
		return nil, err
	}
	poll := f.Poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	lines, offset := lastLines(fh, tail(q))
	ch := make(chan host.LogEntry, 64)
	go func() {
		defer close(ch)
		defer fh.Close()
		send := func(s string) bool {
			e := ParseLine(s)
			if !q.Since.IsZero() && !e.Time.IsZero() && e.Time.Before(q.Since) {
				return true
			}
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, l := range lines {
			if !send(l) {
				return
			}
		}
		if !q.Follow {
			return
		}
		var partial []byte
		buf := make([]byte, 32*1024)
		for {
			n, err := fh.ReadAt(buf, offset)
			if n > 0 {
				offset += int64(n)
				partial = append(partial, buf[:n]...)
				for {
					i := indexByte(partial, '\n')
					if i < 0 {
						break
					}
					if l := string(partial[:i]); l != "" && !send(l) {
						return
					}
					partial = partial[i+1:]
				}
				continue
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return
			}
			if fi, err := fh.Stat(); err == nil && fi.Size() < offset {
				offset = 0 // truncated or rotated in place
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(poll):
			}
		}
	}()
	return ch, nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// lastLines returns up to n complete trailing lines and the offset after them.
func lastLines(f *os.File, n int) ([]string, int64) {
	fi, err := f.Stat()
	if err != nil {
		return nil, 0
	}
	size := fi.Size()
	const chunk = 64 * 1024
	var data []byte
	pos := size
	for pos > 0 {
		step := int64(chunk)
		if pos < step {
			step = pos
		}
		pos -= step
		b := make([]byte, step)
		if _, err := f.ReadAt(b, pos); err != nil && !errors.Is(err, io.EOF) {
			break
		}
		data = append(b, data...)
		if countNL(data) > n {
			break
		}
	}
	// Drop a trailing partial line; it will be read when following.
	end := len(data)
	for end > 0 && data[end-1] != '\n' {
		end--
	}
	consumed := size - int64(len(data)-end)
	var lines []string
	start := 0
	for i := 0; i < end; i++ {
		if data[i] == '\n' {
			if i > start {
				lines = append(lines, string(data[start:i]))
			}
			start = i + 1
		}
	}
	if pos > 0 && len(lines) > 0 {
		lines = lines[1:] // first line may be cut
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, consumed
}

func countNL(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
