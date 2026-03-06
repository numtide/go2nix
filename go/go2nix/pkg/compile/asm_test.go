package compile

import (
	"slices"
	"testing"
)

func TestAsmBaseArgs(t *testing.T) {
	opts := Options{
		PFlag:       "example.com/foo",
		TrimPath:    "/build",
		goroot:      "/usr/local/go",
		goos:        "linux",
		goarch:      "amd64",
		asmArchDefs: []string{"-D", "GOAMD64_v1"},
	}

	got := asmBaseArgs(opts)

	// Check required flags are present.
	expect := []struct {
		flag, value string
	}{
		{"-p", "example.com/foo"},
		{"-trimpath", "/build"},
	}
	for _, e := range expect {
		idx := slices.Index(got, e.flag)
		if idx < 0 || idx+1 >= len(got) || got[idx+1] != e.value {
			t.Errorf("expected %s %s in args %v", e.flag, e.value, got)
		}
	}

	// Check -I includes both TrimPath and GOROOT/pkg/include.
	iFlags := []string{}
	for i, f := range got {
		if f == "-I" && i+1 < len(got) {
			iFlags = append(iFlags, got[i+1])
		}
	}
	if !slices.Contains(iFlags, "/build") {
		t.Errorf("expected -I /build, got -I flags: %v", iFlags)
	}
	if !slices.Contains(iFlags, "/usr/local/go/pkg/include") {
		t.Errorf("expected -I /usr/local/go/pkg/include, got -I flags: %v", iFlags)
	}

	// Check GOOS/GOARCH defines.
	if !slices.Contains(got, "GOOS_linux") {
		t.Errorf("expected GOOS_linux in %v", got)
	}
	if !slices.Contains(got, "GOARCH_amd64") {
		t.Errorf("expected GOARCH_amd64 in %v", got)
	}

	// Check arch-specific defines are appended.
	if !slices.Contains(got, "GOAMD64_v1") {
		t.Errorf("expected GOAMD64_v1 in %v", got)
	}
}
