package serverconfig

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/tonhe/headsup/internal/host/hsconfig"
)

// Move is one key handled by Migrate.
type Move struct {
	From, To string // To is "" when the key was removed
	Skipped  string // why the key was left alone, "" when moved
}

func (mv Move) String() string {
	switch {
	case mv.Skipped != "":
		return fmt.Sprintf("%s left in place: %s", mv.From, mv.Skipped)
	case mv.To == "":
		return mv.From + " removed (no longer used)"
	}
	return mv.From + " -> " + mv.To
}

// deprecatedPresent lists the deprecated or removed keys doc still sets for
// version, in catalog order.
func deprecatedPresent(doc *hsconfig.Doc, version string) []string {
	if doc == nil {
		return nil
	}
	deps := hsconfig.Deprecations(version)
	var out []string
	for _, x := range hsconfig.All() {
		if _, ok := deps[x.Key]; ok && doc.Has(x.Key) {
			out = append(out, x.Key)
		}
	}
	return out
}

// Migrate moves every deprecated key in src to its replacement for version
// (for example 0.29's flat tls_cert_path to 0.30's tls.cert_path) and drops
// keys that version no longer reads. Comments elsewhere survive. A key whose
// replacement is already set to a different value is left alone and
// reported, so nothing the admin wrote is silently lost.
func Migrate(src []byte, version string) ([]byte, []Move, error) {
	doc, err := hsconfig.Parse(src)
	if err != nil {
		return nil, nil, err
	}
	deps := hsconfig.Deprecations(version)
	var moves []Move
	for _, from := range deprecatedPresent(doc, version) {
		to := deps[from]
		n, _ := doc.Node(from)
		if to == "" {
			if err := doc.Delete(from); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", from, err)
			}
			moves = append(moves, Move{From: from})
			continue
		}
		if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
			if err := doc.Delete(from); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", from, err)
			}
			moves = append(moves, Move{From: from, To: to})
			continue
		}
		if n.Kind != yaml.ScalarNode && n.Kind != yaml.SequenceNode {
			moves = append(moves, Move{From: from, To: to, Skipped: "not a plain value; move it in $EDITOR"})
			continue
		}
		old, _ := doc.Get(from)
		if cur, ok := doc.Get(to); ok && cur != "" && cur != old {
			moves = append(moves, Move{From: from, To: to, Skipped: fmt.Sprintf("%s is already set to a different value", to)})
			continue
		}
		if n.Kind == yaml.SequenceNode {
			l, _ := doc.List(from)
			err = doc.SetList(to, l)
		} else {
			err = doc.Set(to, n.Value)
		}
		if err == nil {
			err = doc.Delete(from)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s -> %s: %w", from, to, err)
		}
		moves = append(moves, Move{From: from, To: to})
	}
	return doc.Bytes(), moves, nil
}
