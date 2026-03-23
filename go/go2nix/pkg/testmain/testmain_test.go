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
