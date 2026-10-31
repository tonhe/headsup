package derp

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/derpmap"
	"github.com/tonhe/headsup/internal/module"
)

// newFileAction stages a new DERP map file holding one custom region and
// adds it to derp.paths: one change set, the file written first.
func (m *Model) newFileAction() module.Action {
	return module.Action{
		ID: "derp.newfile", Title: "New DERP map file", Keys: []string{"N"}, Mutating: true, Needs: host.NeedConfigW,
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := m.ready(ctx); !ok {
				return ok, why
			}
			if m.sec != SecMaps && m.sec != SecSources {
				return false, "Switch to Map files (tab) to create a map file."
			}
			return true, ""
		},
		Params: func(module.Context) *module.FormSpec {
			def := filepath.Join(filepath.Dir(m.cfg.Path), "derp.yaml")
			fs := []module.Field{{ID: "path", Label: "New file", Value: def, Required: true, Validate: checkAbs,
				Help: "The extension picks the format: .yaml (headscale's keys) or .json/.hujson (Tailscale's)"}}
			fs = append(fs, regionFields(derpmap.Region{RegionID: 900})...)
			fs = append(fs, nodeFields(derpmap.Node{Name: "900a"})...)
			return &module.FormSpec{Title: "New DERP map file", Submit: "Stage", Fields: fs}
		},
		Run: func(ctx module.Context, v module.Values) tea.Cmd {
			path := filepath.Clean(strings.TrimSpace(v["path"]))
			r := regionFrom(v)
			r.Nodes = []derpmap.Node{nodeFrom(v, r.RegionID)}
			h := ctx.Host()
			return func() tea.Msg {
				_, err := h.Open(host.FileDERPMap, path)
				switch {
				case err == nil:
					return newFileCheckedMsg{path: path, err: fmt.Errorf("%s already exists; add it under Map sources instead", path)}
				case errors.Is(err, fs.ErrNotExist):
					return newFileCheckedMsg{path: path, region: r}
				}
				return newFileCheckedMsg{path: path, err: err}
			}
		},
	}
}

func (m *Model) finishNewFile(ctx module.Context, msg newFileCheckedMsg) tea.Cmd {
	if msg.err != nil {
		return failed("derp.newfile", msg.err)
	}
	paths, _ := m.doc.List(keyPaths)
	if slices.Contains(paths, msg.path) {
		return failed("derp.newfile", fmt.Errorf("%s is already in derp.paths", msg.path))
	}
	f := derpmap.NewFile(msg.path)
	if err := (derpmap.Map{}).CheckNewRegion(msg.region, 0); err != nil {
		return failed("derp.newfile", err)
	}
	if err := f.PutRegion(msg.region); err != nil {
		return failed("derp.newfile", err)
	}
	if err := m.setSources(keyPaths, append(paths, msg.path)); err != nil {
		return failed("derp.newfile", err)
	}
	m.files = append(m.files, &mapFile{path: msg.path, m: f, dirty: true,
		file: &host.File{Kind: host.FileDERPMap, Path: msg.path, New: true, Effect: host.EffectRestart,
			Mode: m.cfg.Mode, UID: m.cfg.UID, GID: m.cfg.GID}})
	m.sec = SecMaps
	return tea.Batch(m.staged(ctx), ok("derp.newfile",
		fmt.Sprintf("Staged %s with region %d and added it to derp.paths.", msg.path, msg.region.RegionID)))
}
