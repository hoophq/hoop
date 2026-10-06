package inspect_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// Operations is a hand-written list over libhoop's constants. This parses
// libhoop's source so a new operation there fails here, instead of being
// refused in every trigger that names it.
func TestOperationsMatchLibhoop(t *testing.T) {
	pkg, err := build.Import("github.com/hoophq/libhoop/v2/codec/types", ".", build.FindOnly)
	if err != nil {
		t.Fatalf("locating libhoop: %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, "types.go"), nil, 0)
	if err != nil {
		t.Fatalf("parsing libhoop: %v", err)
	}
	var want []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		if id, ok := spec.Type.(*ast.Ident); !ok || id.Name != "Operation" {
			return true
		}
		for _, v := range spec.Values {
			if lit, ok := v.(*ast.BasicLit); ok {
				s, _ := strconv.Unquote(lit.Value)
				want = append(want, s)
			}
		}
		return true
	})
	if len(want) == 0 {
		t.Fatal("found no Operation constants in libhoop; the parse is wrong")
	}
	var got []string
	for _, op := range inspect.Operations() {
		got = append(got, string(op))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("inspect.Operations() = %v\nlibhoop defines   %v", got, want)
	}
}
