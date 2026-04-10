package testmain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateBasic(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte(`package foo

import "testing"

func TestAdd(t *testing.T) {}
func BenchmarkAdd(b *testing.B) {}
func FuzzAdd(f *testing.F) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Generate(Options{
		ImportPath:  "example.com/foo",
		TestGoFiles: []string{testFile},
	})
	if err != nil {
		t.Fatal(err)
	}

	src := string(got)
	if !strings.Contains(src, `_test "example.com/foo"`) {
		t.Error("missing internal test import")
	}
	if !strings.Contains(src, `{"TestAdd", _test.TestAdd}`) {
		t.Error("missing TestAdd")
	}
	if !strings.Contains(src, `{"BenchmarkAdd", _test.BenchmarkAdd}`) {
		t.Error("missing BenchmarkAdd")
	}
	if !strings.Contains(src, `{"FuzzAdd", _test.FuzzAdd}`) {
		t.Error("missing FuzzAdd")
	}
	if !strings.Contains(src, "testing.MainStart") {
		t.Error("missing MainStart call")
	}
}

func writeTestFile(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "x_test.go")
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGenerateBadSignature(t *testing.T) {
	cases := []struct {
		name, src, wantArg string
	}{
		{"Test", "func TestBad(x int) {}\n", "t *testing.T"},
		{"Benchmark", "func BenchmarkBad() {}\n", "b *testing.B"},
		{"Fuzz", "func FuzzBad(f *testing.T) {}\n", "f *testing.F"},
		{"TestMain", "func TestMain() {}\n", "m *testing.M"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := writeTestFile(t, "package foo\nimport \"testing\"\nvar _ = testing.Short\n"+tc.src)
			_, err := Generate(Options{ImportPath: "example.com/foo", TestGoFiles: []string{f}})
			if err == nil {
				t.Fatalf("expected error for bad %s signature, got nil", tc.name)
			}
			msg := err.Error()
			if !strings.Contains(msg, "wrong signature for") || !strings.Contains(msg, tc.wantArg) {
				t.Errorf("error %q does not match upstream format (want %q)", msg, tc.wantArg)
			}
			if !strings.Contains(msg, "x_test.go:") {
				t.Errorf("error %q missing file:line position", msg)
			}
		})
	}
}

func TestGenerateGenericTestFunc(t *testing.T) {
	f := writeTestFile(t, "package foo\nimport \"testing\"\nfunc TestGeneric[T any](t *testing.T) { _ = t }\n")
	_, err := Generate(Options{ImportPath: "example.com/foo", TestGoFiles: []string{f}})
	if err == nil {
		t.Fatal("expected error for generic test func, got nil")
	}
	if !strings.Contains(err.Error(), "test functions cannot have type parameters") {
		t.Errorf("error %q does not mention type parameters", err.Error())
	}
}

func TestGenerateModulePath(t *testing.T) {
	got, err := Generate(Options{
		ImportPath: "example.com/foo/bar",
		ModulePath: "example.com/foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	src := string(got)
	if !strings.Contains(src, `testdeps.ModulePath = "example.com/foo"`) {
		t.Error("missing testdeps.ModulePath assignment")
	}
	if !strings.Contains(src, `testdeps.ImportPath = "example.com/foo/bar"`) {
		t.Error("missing testdeps.ImportPath assignment")
	}
	if strings.Index(src, "testdeps.ModulePath") > strings.Index(src, "testdeps.ImportPath") {
		t.Error("ModulePath should be set before ImportPath (matches upstream template)")
	}
}

func TestGenerateExternal(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "foo_ext_test.go")
	if err := os.WriteFile(testFile, []byte(`package foo_test

import "testing"

func TestExternal(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Generate(Options{
		ImportPath:   "example.com/foo",
		XTestGoFiles: []string{testFile},
	})
	if err != nil {
		t.Fatal(err)
	}

	src := string(got)
	if !strings.Contains(src, `_xtest "example.com/foo_test"`) {
		t.Error("missing external test import")
	}
	if !strings.Contains(src, `{"TestExternal", _xtest.TestExternal}`) {
		t.Error("missing TestExternal")
	}
}

func TestGenerateTestMain(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main_test.go")
	if err := os.WriteFile(testFile, []byte(`package foo

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestFoo(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Generate(Options{
		ImportPath:  "example.com/foo",
		TestGoFiles: []string{testFile},
	})
	if err != nil {
		t.Fatal(err)
	}

	src := string(got)
	if !strings.Contains(src, "_test.TestMain(m)") {
		t.Error("missing TestMain call")
	}
	if !strings.Contains(src, `"reflect"`) {
		t.Error("missing reflect import for TestMain")
	}
	if !strings.Contains(src, `{"TestFoo", _test.TestFoo}`) {
		t.Error("missing TestFoo")
	}
}

func TestGenerateDuplicateTestMain(t *testing.T) {
	dir := t.TempDir()
	intFile := filepath.Join(dir, "a_test.go")
	if err := os.WriteFile(intFile, []byte(`package foo

import "testing"

func TestMain(m *testing.M) { m.Run() }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	extFile := filepath.Join(dir, "b_test.go")
	if err := os.WriteFile(extFile, []byte(`package foo_test

import "testing"

func TestMain(m *testing.M) { m.Run() }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Generate(Options{
		ImportPath:   "example.com/foo",
		TestGoFiles:  []string{intFile},
		XTestGoFiles: []string{extFile},
	})
	if err == nil {
		t.Fatal("expected error for duplicate TestMain, got nil")
	}
	if !strings.Contains(err.Error(), "multiple definitions of TestMain") {
		t.Errorf("expected 'multiple definitions of TestMain' in error, got: %v", err)
	}
}

func TestGenerateExamples(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "example_test.go")
	if err := os.WriteFile(testFile, []byte(`package foo

import "fmt"

func ExampleHello() {
	fmt.Println("hello")
	// Output: hello
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Generate(Options{
		ImportPath:  "example.com/foo",
		TestGoFiles: []string{testFile},
	})
	if err != nil {
		t.Fatal(err)
	}

	src := string(got)
	if !strings.Contains(src, `"ExampleHello"`) {
		t.Error("missing ExampleHello")
	}
	if !strings.Contains(src, `"hello\n"`) {
		t.Error("missing example output")
	}
}

func TestGenerateEmpty(t *testing.T) {
	got, err := Generate(Options{
		ImportPath: "example.com/foo",
	})
	if err != nil {
		t.Fatal(err)
	}

	src := string(got)
	if !strings.Contains(src, "package main") {
		t.Error("missing package main")
	}
	if !strings.Contains(src, "testing.MainStart") {
		t.Error("missing MainStart")
	}
}

func TestGenerateMixed(t *testing.T) {
	dir := t.TempDir()
	intFile := filepath.Join(dir, "foo_test.go")
	if err := os.WriteFile(intFile, []byte(`package foo

import "testing"

func TestInternal(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	extFile := filepath.Join(dir, "foo_ext_test.go")
	if err := os.WriteFile(extFile, []byte(`package foo_test

import "testing"

func TestExternal(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Generate(Options{
		ImportPath:   "example.com/foo",
		TestGoFiles:  []string{intFile},
		XTestGoFiles: []string{extFile},
	})
	if err != nil {
		t.Fatal(err)
	}

	src := string(got)
	if !strings.Contains(src, `_test "example.com/foo"`) {
		t.Error("missing internal test import")
	}
	if !strings.Contains(src, `_xtest "example.com/foo_test"`) {
		t.Error("missing external test import")
	}
}
