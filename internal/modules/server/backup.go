package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/backup"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
	"github.com/tonhe/headsup/internal/version"
)

type bundlesMsg struct {
	root string
	list []backup.Bundle
	err  error
}

type backupMsg struct {
	res  backup.Result
	err  error
	root string
	// then is set when the backup precedes a restore.
	then *restoreReq
}

type restoreReq struct {
	id, dir string
}

type restoreMsg struct {
	id  string
	res backup.RestoreResult
	err error
}

func (m *Model) backupOpts(ctx module.Context) backup.Options {
	o := backup.Options{Now: ctx.Now, HeadsupVersion: version.Version}
	if simulated(ctx) {
		o.Root, o.DB, o.Simulated = backup.DemoRoot(), backup.SimulatedDB, true
	} else {
		o.Root = backup.DefaultRoot(m.hostSettings(ctx).BackupDir)
	}
	if m.root != "" {
		o.Root = m.root
	}
	return o
}

func (m *Model) listBackups(ctx module.Context) tea.Cmd {
	root := m.backupOpts(ctx).Root
	return func() tea.Msg {
		l, err := backup.List(root)
		return bundlesMsg{root: root, list: l, err: err}
	}
}

func (m *Model) onBundles(msg bundlesMsg) {
	m.bundles, m.bkErr = msg.list, msg.err
	rows := make([]table.Row, len(msg.list))
	for i, b := range msg.list {
		rows[i] = bundleRow{b: b}
	}
	m.bks.SetRows(rows)
}

func (m *Model) backupAction() module.Action {
	return module.Action{
		ID: "server.backup", Title: "Back up now", Keys: []string{"b"}, Needs: host.NeedConfigR,
		Targets: func(ctx module.Context) []module.Target {
			return []module.Target{{Kind: "backup", Name: m.backupOpts(ctx).Root}}
		},
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := hostOnly(ctx); !ok {
				return ok, why
			}
			if m.bkBusy != "" {
				return false, "A backup or restore is already running."
			}
			return true, ""
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			m.tab, m.bkBusy, m.restored = tabBackups, "backing up", nil
			return m.backupCmd(ctx, nil)
		},
	}
}

func (m *Model) backupCmd(ctx module.Context, then *restoreReq) tea.Cmd {
	h, o := ctx.Host(), m.backupOpts(ctx)
	return func() tea.Msg {
		c, cancel := context.WithTimeout(bg(), 10*time.Minute)
		defer cancel()
		res, err := backup.Backup(c, h, o)
		return backupMsg{res: res, err: err, root: o.Root, then: then}
	}
}

func (m *Model) onBackup(msg backupMsg) tea.Cmd {
	id := "server.backup"
	if msg.then != nil {
		id = "server.restore"
	}
	if msg.err != nil {
		m.bkBusy = ""
		err := msg.err
		if msg.then != nil {
			err = fmt.Errorf("restore not started, the safety backup failed: %w", err)
		}
		return func() tea.Msg { return module.ActionResult{ActionID: id, Err: err} }
	}
	r := msg.res
	m.last = &r
	root := msg.root
	list := func() tea.Msg {
		l, err := backup.List(root)
		return bundlesMsg{root: root, list: l, err: err}
	}
	if msg.then != nil {
		m.bkBusy = "restoring"
		return tea.Batch(list, m.restoreCmd(*msg.then))
	}
	m.bkBusy = ""
	m.bks.SetCursorKey(r.Manifest.ID)
	text := r.Summary()
	return tea.Batch(list, func() tea.Msg { return module.ActionResult{ActionID: id, Message: text} })
}

// restoreRun is the host and options captured when a restore starts.
type restoreRun struct {
	h host.Host
	o backup.Options
}

func (m *Model) restoreAction() module.Action {
	return module.Action{
		ID: "server.restore", Title: "Restore files from the selected backup", Keys: []string{"r"},
		Mutating: true, Danger: module.DangerCritical, Needs: host.NeedConfigW,
		Consequence: "Files are put back as they were in this backup, after a fresh backup of the current ones. The database is not touched; its restore steps are shown.",
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := hostOnly(ctx); !ok {
				return ok, why
			}
			if _, ok := m.bundle(); !ok || m.tab != tabBackups {
				return false, "Select a backup first (B lists them)."
			}
			if m.bkBusy != "" {
				return false, "A backup or restore is already running."
			}
			return true, ""
		},
		Targets: func(module.Context) []module.Target {
			b, ok := m.bundle()
			if !ok {
				return nil
			}
			return []module.Target{{Kind: "backup", ID: b.ID, Name: b.ID}}
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			b, ok := m.bundle()
			if !ok {
				return nil
			}
			m.bkBusy, m.restored = "backing up first", nil
			m.run = &restoreRun{h: ctx.Host(), o: m.backupOpts(ctx)}
			return m.backupCmd(ctx, &restoreReq{id: b.ID, dir: b.Dir})
		},
	}
}

func (m *Model) restoreCmd(req restoreReq) tea.Cmd {
	rr := m.run
	if rr == nil {
		return nil
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(bg(), 5*time.Minute)
		defer cancel()
		res, err := backup.Restore(c, rr.h, req.dir, rr.o)
		return restoreMsg{id: req.id, res: res, err: err}
	}
}

func (m *Model) onRestore(msg restoreMsg) tea.Cmd {
	m.bkBusy = ""
	if msg.err == nil {
		r := msg.res
		m.restored = &r
		m.restoredID = msg.id
	}
	err := msg.err
	text := ""
	if err == nil {
		text = fmt.Sprintf("Restored %d files from %s (%d already matched).", len(msg.res.Restored), msg.id, len(msg.res.Unchanged))
		if !msg.res.OK() {
			paths := make([]string, 0, len(msg.res.Failed))
			for p := range msg.res.Failed {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			err = fmt.Errorf("restored %d files from %s; %d failed: %s", len(msg.res.Restored), msg.id, len(paths), strings.Join(paths, ", "))
		}
	}
	return func() tea.Msg { return module.ActionResult{ActionID: "server.restore", Err: err, Message: text} }
}

func (m *Model) bundle() (backup.Bundle, bool) {
	if m.bks == nil {
		return backup.Bundle{}, false
	}
	r, ok := m.bks.Cursor()
	if !ok {
		return backup.Bundle{}, false
	}
	return r.(bundleRow).b, true
}

type bundleRow struct {
	b   backup.Bundle
	now time.Time
}

func (r bundleRow) Key() string { return r.b.ID }
func (r bundleRow) Cell(col string) string {
	switch col {
	case "id":
		return r.b.ID
	case "age":
		if r.now.IsZero() {
			return r.b.Created.UTC().Format("2006-01-02 15:04")
		}
		return humanize.Ago(r.b.Created, r.now)
	case "files":
		s := fmt.Sprint(r.b.Files)
		if r.b.Missing > 0 {
			s += fmt.Sprintf(" (%d missing)", r.b.Missing)
		}
		return s
	case "db":
		switch r.b.DB {
		case backup.MethodSQLiteBackupAPI:
			return "online copy"
		case backup.MethodPgDump:
			return "pg_dump steps"
		case backup.MethodSimulated:
			return "simulated"
		}
		return "none"
	}
	return ""
}
func (r bundleRow) SortValue(col string) string { return r.b.ID }

func backupColumns() []table.Column {
	return []table.Column{
		{Key: "id", Title: "Backup", Width: 20},
		{Key: "age", Title: "Taken", Width: 14, Priority: 2},
		{Key: "files", Title: "Files", Width: 14, Priority: 1},
		{Key: "db", Title: "Database", Width: 14, MinWidth: 8, Flex: true},
	}
}

func (m *Model) backupsView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if ctx.Host() == nil {
		return hostView(ctx, "Backups", "Backup and restore", w, h)
	}
	rows := make([]table.Row, len(m.bundles))
	for i, b := range m.bundles {
		rows[i] = bundleRow{b: b, now: ctx.Now()}
	}
	m.bks.SetRows(rows)
	detailH := min(10, max(4, h/2))
	listH := h - detailH
	m.bks.SetSize(max(0, w-2), max(0, listH-2))
	root := m.backupOpts(ctx).Root
	extra := s.Muted.Render(root)
	if m.bkBusy != "" {
		extra = s.Warn.Render(m.bkBusy + ic.Ellipsis)
	}
	var list string
	iw, _ := pane.Inner(w, listH)
	switch {
	case m.bkErr != nil:
		list = pane.Render(s, "Backups", pane.Fit(s.Danger.Render(m.bkErr.Error()), iw), w, listH, true, pane.WithExtra(extra))
	case len(m.bundles) == 0:
		list = pane.Render(s, "Backups", pane.Fit(s.Muted.Render("No backups yet. Press b to take one."), iw), w, listH, true, pane.WithExtra(extra))
	default:
		list = pane.Render(s, "Backups", m.bks.View(), w, listH, true, pane.NoPad(), pane.WithExtra(extra))
	}

	dw, dh := pane.Inner(w, detailH)
	var lines []string
	add := func(l string) { lines = append(lines, pane.Fit(l, dw)) }
	if b, ok := m.bundle(); ok {
		add(s.Label.Render("Directory ") + s.Text.Render(b.Dir))
		if m.restored != nil && m.restoredID == b.ID {
			r := m.restored
			add(s.OK.Render(fmt.Sprintf("%s restored %d files, %d already matched", ic.Check, len(r.Restored), len(r.Unchanged))))
			for p, err := range r.Failed {
				add(s.Danger.Render(ic.Cross+" "+p+": ") + s.Text.Render(errText(err)))
			}
			for _, n := range r.Notes {
				add(s.Muted.Render(n))
			}
			for _, st := range r.DBSteps {
				for _, l := range wrap(st, dw) {
					add(s.Accent.Render(l))
				}
			}
		} else if m.last != nil && m.last.Manifest.ID == b.ID {
			for _, st := range m.last.DBSteps {
				for _, l := range wrap(st, dw) {
					add(s.Muted.Render(l))
				}
			}
			for _, n := range m.last.Notes {
				add(s.Muted.Render(n))
			}
		} else {
			add(s.Muted.Render("r restores the files (a fresh backup is taken first); the database restore is given as steps."))
		}
	}
	if len(lines) > dh {
		lines = lines[:dh]
	}
	return list + "\n" + pane.Render(s, "Backup", strings.Join(lines, "\n"), w, detailH, false)
}

func errText(err error) string {
	var ie *host.ErrInvalid
	if errors.As(err, &ie) {
		return ie.Error()
	}
	return err.Error()
}
