// Package codeview renders source text with chroma syntax highlighting, a
// line-number gutter, and an error-line marker. Colours come from the active
// theme's Base16 syntax slots, so highlighting follows the theme.
package codeview

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Palette maps chroma token categories to styles derived from a theme,
// following the Base16 styling guidelines: comments base03, keys base0D,
// strings base0B, constants and numbers base09, keywords base0E, operators
// and punctuation base05, errors base08.
type Palette struct {
	byType  map[chroma.TokenType]lipgloss.Style
	Default lipgloss.Style
}

// NewPalette builds the token styles for t.
func NewPalette(t theme.Theme) Palette {
	fg := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	p := Palette{Default: fg(t.Base05), byType: map[chroma.TokenType]lipgloss.Style{
		chroma.Comment:         fg(t.Base03).Italic(true),
		chroma.CommentPreproc:  fg(t.Base0F),
		chroma.Keyword:         fg(t.Base0E),
		chroma.KeywordConstant: fg(t.Base09),
		chroma.Name:            fg(t.Base05),
		chroma.NameTag:         fg(t.Base0D),
		chroma.NameAttribute:   fg(t.Base0D),
		chroma.NameFunction:    fg(t.Base0D),
		chroma.NameBuiltin:     fg(t.Base0C),
		chroma.LiteralString:   fg(t.Base0B),
		chroma.LiteralNumber:   fg(t.Base09),
		chroma.Literal:         fg(t.Base0B),
		chroma.Operator:        fg(t.Base05),
		chroma.Punctuation:     fg(t.Base05),
		chroma.Error:           fg(t.Base08),
		chroma.GenericInserted: fg(t.Base0B),
		chroma.GenericDeleted:  fg(t.Base08),
	}}
	return p
}

// Style returns the style for a token type, falling back to its parents.
func (p Palette) Style(tt chroma.TokenType) lipgloss.Style {
	for _, c := range []chroma.TokenType{tt, tt.SubCategory(), tt.Category()} {
		if s, ok := p.byType[c]; ok {
			return s
		}
	}
	return p.Default
}

// lexerFor picks a lexer; HuJSON is JSON with comments, which chroma's JSON
// lexer accepts for line comments.
func lexerFor(lang string) chroma.Lexer {
	l := lexers.Get(lang)
	if l == nil {
		l = lexers.Fallback
	}
	return chroma.Coalesce(l)
}

// Highlight tokenises src and returns one styled string per line. On a lexer
// failure it returns the lines unstyled.
func Highlight(p Palette, lang, src string) []string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	plain := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	it, err := lexerFor(lang).Tokenise(nil, src)
	if err != nil {
		return plain
	}
	var (
		out []string
		cur strings.Builder
	)
	for tok := it(); tok != chroma.EOF; tok = it() {
		st := p.Style(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			if part != "" {
				cur.WriteString(st.Render(expandTabs(part)))
			}
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	// The lexer may drop or add a trailing empty line; keep the source's count.
	for len(out) < len(plain) {
		out = append(out, "")
	}
	return out[:len(plain)]
}

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// Options control a render.
type Options struct {
	// Offset is the first visible line, 0-based.
	Offset int
	// ErrLine is the 1-based line to mark as an error; 0 for none.
	ErrLine int
	// Cursor is the 1-based line to mark as selected; 0 for none.
	Cursor int
	// Marker is the error marker glyph (default "▶").
	Marker string
	// NoNumbers hides the line-number gutter.
	NoNumbers bool
}

// View caches the highlighted lines of one text so scrolling does not
// re-tokenise. The zero value is ready to use.
type View struct {
	src, lang string
	theme     string
	lines     []string
}

// Set updates the text; it re-highlights only when the text or theme changed.
func (v *View) Set(s *styles.Styles, lang, src string) {
	if v.lines != nil && v.src == src && v.lang == lang && v.theme == s.Theme.Slug {
		return
	}
	v.src, v.lang, v.theme = src, lang, s.Theme.Slug
	v.lines = Highlight(NewPalette(s.Theme), lang, src)
}

// Lines reports how many lines the text has.
func (v *View) Lines() int { return len(v.lines) }

// Render draws the visible window at exactly w x h.
func (v *View) Render(s *styles.Styles, w, h int, o Options) string {
	return Render(s, v.lines, w, h, o)
}

// ClampOffset keeps an offset within the scrollable range.
func ClampOffset(off, lines, h int) int {
	return max(0, min(off, lines-h))
}

// Render draws highlighted lines with a gutter at exactly w x h.
func Render(s *styles.Styles, lines []string, w, h int, o Options) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	marker := o.Marker
	if marker == "" {
		marker = "▶"
	}
	numW := len(fmt.Sprint(len(lines)))
	off := ClampOffset(o.Offset, len(lines), h)
	out := make([]string, 0, h)
	for i := off; i < off+h; i++ {
		if i >= len(lines) {
			out = append(out, strings.Repeat(" ", w))
			continue
		}
		n := i + 1
		var g string
		if !o.NoNumbers {
			num := fmt.Sprintf("%*d", numW, n)
			switch {
			case n == o.ErrLine:
				g = s.Danger.Render(pane.Fit(marker, 1)+" "+num) + " " + s.Danger.Render("│")
			case n == o.Cursor:
				g = s.Accent.Render("› "+num) + " " + s.Muted.Render("│")
			default:
				g = s.Muted.Render("  "+num+" │")
			}
		}
		body := lines[i]
		if o.NoNumbers && n == o.ErrLine {
			body = s.Danger.Render(marker+" ") + body
		}
		if g != "" {
			body = g + " " + body
		}
		out = append(out, pane.Fit(body, w))
	}
	return strings.Join(out, "\n")
}
