package compile

import (
	"slices"
	"testing"
)

func TestAsmArchDefines(t *testing.T) {
	tests := []struct {
		name   string
		goarch string
		envKey string
		envVal string
		want   []string
	}{
		{"amd64 with GOAMD64", "amd64", "GOAMD64", "v3", []string{"-D", "GOAMD64_v3"}},
		{"386 with GO386", "386", "GO386", "sse2", []string{"-D", "GO386_sse2"}},
		{"arm7", "arm", "GOARM", "7", []string{"-D", "GOARM_7", "-D", "GOARM_6", "-D", "GOARM_5"}},
		{"arm6", "arm", "GOARM", "6", []string{"-D", "GOARM_6", "-D", "GOARM_5"}},
		{"arm default", "arm", "GOARM", "", []string{"-D", "GOARM_5"}},
		{"arm64 with lse", "arm64", "GOARM64", "v8.0,lse", []string{"-D", "GOARM64_LSE"}},
		{"arm64 without lse", "arm64", "GOARM64", "v8.0", nil},
		{"mips", "mips", "GOMIPS", "softfloat", []string{"-D", "GOMIPS_softfloat"}},
		{"mips64", "mips64", "GOMIPS64", "hardfloat", []string{"-D", "GOMIPS64_hardfloat"}},
		{"ppc64 power10", "ppc64", "GOPPC64", "power10", []string{"-D", "GOPPC64_power10", "-D", "GOPPC64_power9", "-D", "GOPPC64_power8"}},
		{"ppc64 power9", "ppc64le", "GOPPC64", "power9", []string{"-D", "GOPPC64_power9", "-D", "GOPPC64_power8"}},
		{"ppc64 default", "ppc64", "GOPPC64", "", []string{"-D", "GOPPC64_power8"}},
		{"riscv64", "riscv64", "GORISCV64", "rva20u64", []string{"-D", "GORISCV64_rva20u64"}},
		{"unknown arch", "s390x", "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}
			got := asmArchDefines(tt.goarch)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

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
