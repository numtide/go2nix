//go:build linux

package main

import (
	"debug/elf"
	"debug/gosym"
	"os"
	"strings"
	"testing"
)

// The test binary has to be linked against the same standard library as
// the program. variants.nix says, through the environment, which functions
// that library must and must not contain. They are looked up in the
// pclntab, which a stripped binary keeps.
func TestStdlibOfTestBinary(t *testing.T) {
	// Never true: keeps lookups reachable from the test binary.
	if len(os.Args) == 0 {
		lookups("")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pclnSection, text := f.Section(".gopclntab"), f.Section(".text")
	if pclnSection == nil || text == nil {
		t.Fatal("no .gopclntab or .text section in the test binary")
	}
	pclntab, err := pclnSection.Data()
	if err != nil {
		t.Fatal(err)
	}
	table, err := gosym.NewTable(nil, gosym.NewLineTable(pclntab, text.Addr))
	if err != nil {
		t.Fatal(err)
	}
	if table.LookupFunc("example.com/stdlib-per-build.lookups") == nil {
		t.Fatal("lookups is not in the pclntab: nothing below can be trusted")
	}

	for _, c := range []struct{ env, prefix string }{
		{"WANT_NET_CGO", "net._C"},
		{"WANT_USER_CGO", "os/user._C"},
		{"WANT_TZDATA", "time/tzdata."},
	} {
		want := os.Getenv(c.env)
		if want != "0" && want != "1" {
			t.Fatalf("%s=%q, want 0 or 1", c.env, want)
		}
		got := "0"
		for _, fn := range table.Funcs {
			if strings.HasPrefix(fn.Name, c.prefix) {
				got = "1"
				break
			}
		}
		if got != want {
			t.Errorf("functions with prefix %q: got %s, want %s", c.prefix, got, want)
		}
	}
}
