package dns

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/dnsrec"
	"github.com/tonhe/headsup/internal/module"
)

// defaultRecordsPath suggests where a new records file goes: next to the
// SQLite database (a directory headscale owns and can read), else next to
// the config.
func (m *Model) defaultRecordsPath() string {
	dir := filepath.Dir(m.cfg.Path)
	if db, _ := m.doc.Get("database.sqlite.path"); db != "" && filepath.IsAbs(db) {
		dir = filepath.Dir(db)
	}
	return filepath.Join(dir, "extra-records.json")
}

type moveCheckedMsg struct {
	path string
	err  error
}

// moveAction stages "move inline records to a file": a new records file
// holding the inline records, plus a config edit that drops
// dns.extra_records and sets dns.extra_records_path. Both are written by
// one confirmed apply (records file first), so the config never points at
// a file that does not exist and never holds both keys.
func (m *Model) moveAction() module.Action {
	return module.Action{
		ID: "dns.move", Title: "Move inline records to a file", Keys: []string{"M"}, Mutating: true,
		Needs: host.NeedConfigW | host.NeedRecordsW,
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := m.editable(ctx); !ok {
				return ok, why
			}
			if p := m.config().ExtraRecordsPath; p != "" {
				return false, "Records already live in " + p + "."
			}
			return true, ""
		},
		Params: func(module.Context) *module.FormSpec {
			n := len(m.config().ExtraRecords)
			return &module.FormSpec{Title: "Move inline records to a file", Submit: "Stage", Fields: []module.Field{
				{ID: "path", Label: "New file", Value: m.defaultRecordsPath(), Required: true, Validate: dnsrec.CheckRecordsPath,
					Help: fmt.Sprintf("%d record(s) move here. The extension picks the format (.json, .hujson, .yaml). headscale reloads this file by itself.", n)},
			}}
		},
		Run: func(ctx module.Context, v module.Values) tea.Cmd {
			path := filepath.Clean(v["path"])
			h := ctx.Host()
			return func() tea.Msg {
				_, err := h.Open(host.FileExtraRecords, path)
				switch {
				case err == nil:
					return moveCheckedMsg{path: path, err: fmt.Errorf("%s already exists; choose a new path", path)}
				case errors.Is(err, fs.ErrNotExist):
					return moveCheckedMsg{path: path}
				}
				return moveCheckedMsg{path: path, err: err}
			}
		},
	}
}

// finishMove stages the move once the target is known not to exist.
func (m *Model) finishMove(ctx module.Context, msg moveCheckedMsg) tea.Cmd {
	if msg.err != nil {
		return failed("dns.move", msg.err)
	}
	if m.doc == nil || m.config().ExtraRecordsPath != "" {
		return failed("dns.move", errors.New("the records already moved"))
	}
	f, err := dnsrec.MoveInline(m.doc, msg.path)
	if err != nil {
		return failed("dns.move", err)
	}
	m.recs, m.recDirty, m.recErr = f, true, nil
	m.recF = &host.File{Kind: host.FileExtraRecords, Path: msg.path, New: true, Effect: host.EffectLive,
		Mode: m.cfg.Mode, UID: m.cfg.UID, GID: m.cfg.GID}
	n := len(f.Records)
	return tea.Batch(m.staged(ctx), staged("dns.move",
		fmt.Sprintf("Staged moving %d record(s) to %s and setting dns.extra_records_path.", n, msg.path)))
}
