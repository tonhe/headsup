package host_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from this file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

var escalators = map[string]bool{"sudo": true, "su": true, "doas": true, "pkexec": true}

func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func strLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

func isExecCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" || sel.Sel.Name == "LookPath")
}

// escalationFindings reports privilege-escalation invocations in src: a
// string literal that is exactly sudo/su/doas/pkexec passed to any call or
// placed in a composite literal (an argv), or an exec.Command* whose
// literal argument starts with one (e.g. "sh", "-c", "sudo ..."). Prose
// such as a FixCommand "sudo systemctl restart headscale" is allowed.
func escalationFindings(fset *token.FileSet, f *ast.File) []string {
	var out []string
	report := func(n ast.Node, what string) {
		out = append(out, fset.Position(n.Pos()).String()+": "+what)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			for _, a := range x.Args {
				s, ok := strLit(a)
				if !ok {
					continue
				}
				if escalators[s] {
					report(a, "runs "+strconv.Quote(s))
				} else if fields := strings.Fields(s); isExecCall(x) && len(fields) > 0 && escalators[fields[0]] {
					report(a, "exec of "+strconv.Quote(s))
				}
			}
		case *ast.CompositeLit:
			for _, el := range x.Elts {
				if s, ok := strLit(el); ok && escalators[s] {
					report(el, "argv contains "+strconv.Quote(s))
				}
			}
		}
		return true
	})
	return out
}

func TestNoPrivilegeEscalation(t *testing.T) {
	root := filepath.Join(repoRoot(t), "internal")
	fset := token.NewFileSet()
	for _, p := range goFiles(t, root) {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		for _, finding := range escalationFindings(fset, f) {
			t.Errorf("privilege escalation is forbidden: %s", finding)
		}
	}
}

func TestEscalationMatcher(t *testing.T) {
	tests := []struct {
		src  string
		want int
	}{
		{`package p; func f() { exec.Command("sudo", "systemctl", "restart") }`, 1},
		{`package p; func f() { exec.CommandContext(ctx, "sh", "-c", "sudo rm -rf /") }`, 1},
		{`package p; func f() { run(ctx, "pkexec", "x") }`, 1},
		{`package p; var argv = []string{"doas", "kill"}`, 1},
		{`package p; func f() { r.Run(ctx, "su", "-c", "x") }`, 1},
		{`package p; var fix = "sudo systemctl restart headscale"`, 0},
		{`package p; func f() { fmt.Sprintf("sudo usermod -aG %s $USER", g) }`, 0},
		{`package p; func f() { run(ctx, rf, "sudo systemctl restart headscale", "systemctl", "restart") }`, 0},
		{`package p; func f() { exec.Command("systemctl", "restart", "headscale") }`, 0},
	}
	for _, tt := range tests {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", tt.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(escalationFindings(fset, f)); got != tt.want {
			t.Errorf("%s: %d findings, want %d", tt.src, got, tt.want)
		}
	}
}

var writeSQL = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|ALTER|CREATE|DROP|REPLACE)\b`)

func sqlFindings(fset *token.FileSet, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if bl, ok := n.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			s, err := strconv.Unquote(bl.Value)
			if err != nil {
				s = bl.Value
			}
			if m := writeSQL.FindString(s); m != "" {
				out = append(out, fset.Position(bl.Pos()).String()+": "+m+" in "+strconv.Quote(s))
			}
		}
		return true
	})
	return out
}

func TestNoWriteSQLInDB(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "host", "db")
	fset := token.NewFileSet()
	files := goFiles(t, dir)
	if len(files) == 0 {
		t.Fatal("no files in internal/host/db")
	}
	for _, p := range files {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		for _, finding := range sqlFindings(fset, f) {
			t.Errorf("write SQL is forbidden in internal/host/db: %s", finding)
		}
	}
}

func TestSQLMatcher(t *testing.T) {
	tests := []struct {
		src  string
		want int
	}{
		{"package p; const q = `insert into nodes values (1)`", 1},
		{`package p; var q = "SELECT id FROM nodes; DROP TABLE nodes"`, 1},
		{`package p; var q = "Replace into x"`, 1},
		{`package p; var q = "SELECT id, deleted_at, created_at, updated_at FROM nodes"`, 0},
		{`package p; var q = "default_transaction_read_only"`, 0},
	}
	for _, tt := range tests {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", tt.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(sqlFindings(fset, f)); got != tt.want {
			t.Errorf("%s: %d findings, want %d", tt.src, got, tt.want)
		}
	}
}
