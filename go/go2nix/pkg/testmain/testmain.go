// Test main generator for go2nix.
//
// Discovers Test/Benchmark/Fuzz/Example functions from _test.go files via
// go/parser AST parsing, then generates a package main source that calls
// testing.MainStart(). Logic is adapted from:
//   - go/src/cmd/go/internal/load/test.go (lines 554-852)
//   - go/src/cmd/go/internal/load/test.go:693-764 (load function)
//
// The template is a simplified version of Go's testmainTmpl (no coverage).
package testmain

import (
	"bytes"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"
)

// Options configures test main generation.
type Options struct {
	ImportPath   string   // import path of the package under test
	TestGoFiles  []string // absolute paths to internal _test.go files
	XTestGoFiles []string // absolute paths to external _test.go files
}

// testFunc mirrors cmd/go/internal/load.testFunc.
type testFunc struct {
	Package   string // "_test" or "_xtest"
	Name      string
	Output    string // for examples
	Unordered bool   // for examples
}

// testFuncs holds discovered test functions — simplified from cmd/go/internal/load.testFuncs.
type testFuncs struct {
	Tests       []testFunc
	Benchmarks  []testFunc
	FuzzTargets []testFunc
	Examples    []testFunc
	TestMain    *testFunc
	ImportTest  bool
	NeedTest    bool
	ImportXtest bool
	NeedXtest   bool

	ImportPath string // package under test
}

// Generate produces test main source code for the given options.
func Generate(opts Options) ([]byte, error) {
	t := &testFuncs{
		ImportPath: opts.ImportPath,
	}

	fset := token.NewFileSet()
	for _, file := range opts.TestGoFiles {
		if err := t.load(fset, file, "_test", &t.ImportTest, &t.NeedTest); err != nil {
			return nil, err
		}
	}
	for _, file := range opts.XTestGoFiles {
		if err := t.load(fset, file, "_xtest", &t.ImportXtest, &t.NeedXtest); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := testMainTmpl.Execute(&buf, t); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// load parses a single _test.go file and discovers test functions.
// Adapted from go/src/cmd/go/internal/load/test.go:693-764.
func (t *testFuncs) load(fset *token.FileSet, filename, pkg string, doImport, seen *bool) error {
	src, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return err
	}
	for _, d := range f.Decls {
		n, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if n.Recv != nil {
			continue
		}
		name := n.Name.String()
		switch {
		case name == "TestMain":
			if isTestFunc(n, "T") {
				t.Tests = append(t.Tests, testFunc{pkg, name, "", false})
				*doImport, *seen = true, true
				continue
			}
			if isTestFunc(n, "M") {
				if t.TestMain != nil {
					continue // multiple TestMain — skip
				}
				t.TestMain = &testFunc{pkg, name, "", false}
				*doImport, *seen = true, true
			}
		case isTest(name, "Test"):
			if isTestFunc(n, "T") {
				t.Tests = append(t.Tests, testFunc{pkg, name, "", false})
				*doImport, *seen = true, true
			}
		case isTest(name, "Benchmark"):
			if isTestFunc(n, "B") {
				t.Benchmarks = append(t.Benchmarks, testFunc{pkg, name, "", false})
				*doImport, *seen = true, true
			}
		case isTest(name, "Fuzz"):
			if isTestFunc(n, "F") {
				t.FuzzTargets = append(t.FuzzTargets, testFunc{pkg, name, "", false})
				*doImport, *seen = true, true
			}
		}
	}
	ex := doc.Examples(f)
	sort.Slice(ex, func(i, j int) bool { return ex[i].Order < ex[j].Order })
	for _, e := range ex {
		*doImport = true
		if e.Output == "" && !e.EmptyOutput {
			continue
		}
		t.Examples = append(t.Examples, testFunc{pkg, "Example" + e.Name, e.Output, e.Unordered})
		*seen = true
	}
	return nil
}

// isTestFunc checks if fn has signature func(x *X.arg) with no return values.
// Copied from go/src/cmd/go/internal/load/test.go:556-577.
func isTestFunc(fn *ast.FuncDecl, arg string) bool {
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 ||
		fn.Type.Params.List == nil ||
		len(fn.Type.Params.List) != 1 ||
		len(fn.Type.Params.List[0].Names) > 1 {
		return false
	}
	ptr, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	if name, ok := ptr.X.(*ast.Ident); ok && name.Name == arg {
		return true
	}
	if sel, ok := ptr.X.(*ast.SelectorExpr); ok && sel.Sel.Name == arg {
		return true
	}
	return false
}

// isTest checks Go test naming convention: prefix + optional uppercase letter.
// Copied from go/src/cmd/go/internal/load/test.go:583-592.
func isTest(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}

// testMainTmpl is the test main template.
// Simplified from go/src/cmd/go/internal/load/test.go:781-852 (no coverage).
var testMainTmpl = template.Must(template.New("main").Parse(`// Code generated by go2nix. DO NOT EDIT.

package main

import (
	"os"
{{- if .TestMain}}
	"reflect"
{{- end}}
	"testing"
	"testing/internal/testdeps"

{{- if .ImportTest}}
	{{if .NeedTest}}_test{{else}}_{{end}} {{printf "%q" .ImportPath}}
{{- end}}
{{- if .ImportXtest}}
	{{if .NeedXtest}}_xtest{{else}}_{{end}} {{printf "%s_test" .ImportPath | printf "%q"}}
{{- end}}
)

var tests = []testing.InternalTest{
{{- range .Tests}}
	{"{{.Name}}", {{.Package}}.{{.Name}}},
{{- end}}
}

var benchmarks = []testing.InternalBenchmark{
{{- range .Benchmarks}}
	{"{{.Name}}", {{.Package}}.{{.Name}}},
{{- end}}
}

var fuzzTargets = []testing.InternalFuzzTarget{
{{- range .FuzzTargets}}
	{"{{.Name}}", {{.Package}}.{{.Name}}},
{{- end}}
}

var examples = []testing.InternalExample{
{{- range .Examples}}
	{"{{.Name}}", {{.Package}}.{{.Name}}, {{printf "%q" .Output}}, {{.Unordered}}},
{{- end}}
}

func init() {
	testdeps.ImportPath = {{printf "%q" .ImportPath}}
}

func main() {
	m := testing.MainStart(testdeps.TestDeps{}, tests, benchmarks, fuzzTargets, examples)
{{- if .TestMain}}
	{{.TestMain.Package}}.{{.TestMain.Name}}(m)
	os.Exit(int(reflect.ValueOf(m).Elem().FieldByName("exitCode").Int()))
{{- else}}
	os.Exit(m.Run())
{{- end}}
}
`))
