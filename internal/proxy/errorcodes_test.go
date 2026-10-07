package proxy_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// handlerCodes collects every error code passed to WriteJSON in non-test sources
// under internal/ (the one place that writes the error envelope).
func handlerCodes(t *testing.T) map[string]bool {
	t.Helper()
	codes := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if name != "WriteJSON" {
				return true
			}
			lit, ok := call.Args[2].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: WriteJSON code must be a string literal so docs can be checked", fset.Position(call.Pos()))
				return true
			}
			code, _ := strconv.Unquote(lit.Value)
			codes[code] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return codes
}

// docCodes reads the first column of the table under "## Error codes" in protocol.md.
func docCodes(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../docs/reference/protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("^\\| `([a-z_]+)` \\|")
	codes := map[string]bool{}
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = line == "## Error codes"
			continue
		}
		if m := row.FindStringSubmatch(line); in && m != nil && m[1] != "error" { // "error" is the column header
			codes[m[1]] = true
		}
	}
	return codes
}

// The error code table in protocol.md is the authoritative list: it must name exactly
// the codes the handlers can emit.
func TestErrorCodesDocumented(t *testing.T) {
	code, doc := handlerCodes(t), docCodes(t)
	if len(code) == 0 || len(doc) == 0 {
		t.Fatalf("found %d handler codes and %d documented codes; parser out of date?", len(code), len(doc))
	}
	var missing, stale []string
	for c := range code {
		if !doc[c] {
			missing = append(missing, c)
		}
	}
	for c := range doc {
		if !code[c] {
			stale = append(stale, c)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("emitted by handlers but missing from the protocol.md error table: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("in the protocol.md error table but never emitted: %v", stale)
	}
}
