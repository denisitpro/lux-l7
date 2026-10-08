package route_test

import (
	"go/parser"
	"go/token"
	"testing"
)

// Route parsing runs inside config.Load, before the JSON log handler exists, so it
// must not log: notes travel as Route.Warning and are logged by main.
func TestRouteSourceDoesNotImportSlog(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "route.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		if imp.Path.Value == `"log/slog"` || imp.Path.Value == `"log"` {
			t.Errorf("route.go imports %s", imp.Path.Value)
		}
	}
}
