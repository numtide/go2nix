package cmd

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// Fixture definitions
// ---------------------------------------------------------------------------

type fixtureFile struct {
	Path    string // relative to the fixture root
	Content string // Go source (will be gofmt'd) or plain text
	IsGo    bool   // if true, run go/format on Content before writing
}

type fixture struct {
	Name  string
	Files []fixtureFile
}

var fixtures = []fixture{
	testifyBasic(),
	xtestLocalDep(),
	modrootNested(),
	testHelperPkg(),
}

// ---------------------------------------------------------------------------
// Fixture 1: testify-basic
// ---------------------------------------------------------------------------

func testifyBasic() fixture {
	return fixture{
		Name: "testify-basic",
		Files: []fixtureFile{
			{
				Path: "go.mod",
				Content: `module example.com/testify-basic

go 1.25

require github.com/stretchr/testify v1.10.0
`,
			},
			{
				Path: "main.go",
				IsGo: true,
				Content: `package main

import (
	"fmt"

	"example.com/testify-basic/internal/greeter"
)

func main() {
	fmt.Println(greeter.Greet("world"))
}
`,
			},
			{
				Path: "internal/greeter/greeter.go",
				IsGo: true,
				Content: `package greeter

// Greet returns a greeting for the given name.
func Greet(name string) string {
	return "hello " + name
}
`,
			},
			{
				Path: "internal/greeter/greeter_test.go",
				IsGo: true,
				Content: `package greeter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGreet(t *testing.T) {
	assert.Equal(t, "hello world", Greet("world"))
}
`,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Fixture 2: xtest-local-dep
// ---------------------------------------------------------------------------

func xtestLocalDep() fixture {
	return fixture{
		Name: "xtest-local-dep",
		Files: []fixtureFile{
			{
				Path: "go.mod",
				Content: `module example.com/xtest-local-dep

go 1.25
`,
			},
			{
				Path: "main.go",
				IsGo: true,
				Content: `package main

import (
	"fmt"

	"example.com/xtest-local-dep/internal/handler"
	"example.com/xtest-local-dep/internal/server"
)

func main() {
	fmt.Println(server.Start())
	fmt.Println(handler.Handle("hello"))
}
`,
			},
			{
				Path: "internal/server/server.go",
				IsGo: true,
				Content: `package server

// Start returns the server status.
func Start() string {
	return "running"
}
`,
			},
			{
				Path: "internal/handler/handler.go",
				IsGo: true,
				Content: `package handler

import "example.com/xtest-local-dep/internal/server"

// Handle processes a request using the server.
func Handle(req string) string {
	return server.Start() + ": " + req
}
`,
			},
			{
				Path: "internal/integration/integration_test.go",
				IsGo: true,
				Content: `package integration_test

import (
	"testing"

	"example.com/xtest-local-dep/internal/handler"
	"example.com/xtest-local-dep/internal/server"
)

func TestIntegration(t *testing.T) {
	if server.Start() != "running" {
		t.Fatal("server.Start() != \"running\"")
	}
	if handler.Handle("ping") != "running: ping" {
		t.Fatalf("handler.Handle(\"ping\") = %q, want \"running: ping\"", handler.Handle("ping"))
	}
}
`,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Fixture 3: modroot-nested
// ---------------------------------------------------------------------------

func modrootNested() fixture {
	return fixture{
		Name: "modroot-nested",
		Files: []fixtureFile{
			{
				Path:    "README.md",
				Content: "# modroot-nested test fixture\n",
			},
			{
				Path: "app/go.mod",
				Content: `module example.com/modroot-nested

go 1.25
`,
			},
			{
				Path: "app/main.go",
				IsGo: true,
				Content: `package main

import (
	"fmt"

	"example.com/modroot-nested/internal/util"
)

func main() {
	fmt.Println(util.Version())
}
`,
			},
			{
				Path: "app/internal/util/util.go",
				IsGo: true,
				Content: `package util

// Version returns the application version.
func Version() string {
	return "1.0.0"
}
`,
			},
			{
				Path: "app/internal/util/util_test.go",
				IsGo: true,
				Content: `package util

import "testing"

func TestVersion(t *testing.T) {
	if Version() != "1.0.0" {
		t.Fatalf("Version() = %q, want \"1.0.0\"", Version())
	}
}
`,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Fixture 4: test-helper-pkg
// ---------------------------------------------------------------------------

func testHelperPkg() fixture {
	return fixture{
		Name: "test-helper-pkg",
		Files: []fixtureFile{
			{
				Path: "go.mod",
				Content: `module example.com/test-helper-pkg

go 1.25
`,
			},
			{
				Path: "main.go",
				IsGo: true,
				Content: `package main

import (
	"fmt"

	"example.com/test-helper-pkg/internal/app"
)

func main() {
	fmt.Println(app.Run())
}
`,
			},
			{
				Path: "internal/app/app.go",
				IsGo: true,
				Content: `package app

// Run returns the application status.
func Run() string {
	return "ok"
}
`,
			},
			{
				Path: "internal/app/app_test.go",
				IsGo: true,
				Content: `package app

import (
	"testing"

	"example.com/test-helper-pkg/internal/testutil"
)

func TestRun(t *testing.T) {
	testutil.AssertOK(t, Run())
}
`,
			},
			{
				Path: "internal/testutil/testutil.go",
				IsGo: true,
				Content: `package testutil

import "testing"

// AssertOK fails the test if s is not "ok".
func AssertOK(t *testing.T, s string) {
	t.Helper()
	if s != "ok" {
		t.Fatalf("got %q, want ok", s)
	}
}
`,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// RunFixtures generates all test fixtures under baseDir.
// ---------------------------------------------------------------------------

// RunFixtures generates the test fixture projects into baseDir.
func RunFixtures(baseDir string) error {
	fmt.Printf("Generating %d test fixtures into %s/...\n", len(fixtures), baseDir)

	for _, fix := range fixtures {
		fixDir := filepath.Join(baseDir, fix.Name)
		for _, f := range fix.Files {
			path := filepath.Join(fixDir, f.Path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("mkdir for %s/%s: %w", fix.Name, f.Path, err)
			}

			content := []byte(f.Content)
			if f.IsGo {
				formatted, err := format.Source(content)
				if err != nil {
					return fmt.Errorf("format %s/%s: %w\nsource:\n%s", fix.Name, f.Path, err, f.Content)
				}
				content = formatted
			}

			if err := os.WriteFile(path, content, 0o644); err != nil {
				return fmt.Errorf("write %s/%s: %w", fix.Name, f.Path, err)
			}
		}
		fmt.Printf("  Generated %s (%d files)\n", fix.Name, len(fix.Files))
	}

	fmt.Println("Done.")
	return nil
}
