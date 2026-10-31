package serverconfig

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host/hsconfig"
)

// editedMsg carries the text back from $EDITOR.
type editedMsg struct {
	data []byte
	err  error
}

// editorArgv picks the editor the way most tools do: $VISUAL, then
// $EDITOR, then vi. The value may carry flags ("code -w").
func editorArgv(getenv func(string) string) []string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(getenv(k)); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

// editorSession prepares a private temporary copy of text and the command
// that edits it. done reads the result back and removes the copy; it is the
// tea.ExecProcess callback. The copy may hold secrets, so it is 0600 and
// never outlives the session.
func editorSession(text []byte, getenv func(string) string) (*exec.Cmd, func(error) tea.Msg, error) {
	f, err := os.CreateTemp("", brand.Name+"-config-*.yaml")
	if err != nil {
		return nil, nil, err
	}
	path := f.Name()
	fail := func(e error) (*exec.Cmd, func(error) tea.Msg, error) {
		f.Close()
		os.Remove(path)
		return nil, nil, e
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(text); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	argv := append(editorArgv(getenv), path)
	cmd := exec.Command(argv[0], argv[1:]...)
	done := func(runErr error) tea.Msg {
		defer os.Remove(path)
		if runErr != nil {
			return editedMsg{err: fmt.Errorf("editor %s: %w", argv[0], runErr)}
		}
		b, err := os.ReadFile(path)
		return editedMsg{data: b, err: err}
	}
	return cmd, done, nil
}

// editorCmd suspends the TUI and runs the editor on the staged text.
func editorCmd(text []byte) tea.Cmd {
	cmd, done, err := editorSession(text, os.Getenv)
	if err != nil {
		return func() tea.Msg { return editedMsg{err: err} }
	}
	return tea.ExecProcess(cmd, done)
}

// --- changed on disk ----------------------------------------------------------

// conflict holds the three versions when the file changed since it was
// opened: base (as opened), mine (staged) and theirs (on disk now).
type conflict struct {
	base, mine, theirs []byte
	rebaseErr          error
}

// Rebase re-applies the key-level changes between base and mine onto
// theirs. It refuses when theirs changed one of the same keys differently
// or a change cannot be expressed key by key (lists of mappings). It
// returns the merged text and the keys carried over. Comment-only edits in
// mine are not carried.
func Rebase(base, mine, theirs []byte) ([]byte, []string, error) {
	bd, err := hsconfig.Parse(base)
	if err != nil {
		return nil, nil, fmt.Errorf("original: %w", err)
	}
	md, err := hsconfig.Parse(mine)
	if err != nil {
		return nil, nil, fmt.Errorf("your version: %w", err)
	}
	td, err := hsconfig.Parse(theirs)
	if err != nil {
		return nil, nil, fmt.Errorf("version on disk: %w", err)
	}
	bk, tk := bd.Keys(), td.Keys()
	keys := hsconfig.ChangedKeys(bd, md)
	mk := md.Keys()
	var clash []string
	for _, k := range keys {
		bv, bok := bk[k]
		tv, tok := tk[k]
		mv, mok := mk[k]
		if (tok != bok || tv != bv) && (tok != mok || tv != mv) {
			clash = append(clash, k)
		}
	}
	if len(clash) > 0 {
		return nil, nil, fmt.Errorf("also changed on disk: %s", strings.Join(clash, ", "))
	}
	for _, k := range keys {
		n, ok := md.Node(k)
		if !ok {
			if err := td.Delete(k); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", k, err)
			}
			continue
		}
		switch {
		case n.Kind == yaml.ScalarNode:
			v, _ := md.Get(k)
			err = td.Set(k, v)
		case n.Kind == yaml.SequenceNode && scalarSeq(n):
			l, _ := md.List(k)
			err = td.SetList(k, l)
		default:
			err = errors.New("cannot carry this change over automatically; use $EDITOR (E)")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", k, err)
		}
	}
	return td.Bytes(), keys, nil
}

func scalarSeq(n *yaml.Node) bool {
	for _, c := range n.Content {
		if c.Kind != yaml.ScalarNode {
			return false
		}
	}
	return true
}
