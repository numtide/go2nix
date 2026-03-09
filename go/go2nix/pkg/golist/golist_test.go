package golist

import "testing"

func TestModuleIsLocal(t *testing.T) {
	tests := []struct {
		name string
		mod  *Module
		want bool
	}{
		{"nil module", nil, true},
		{"main module", &Module{Path: "mymod", Main: true}, true},
		{"local replace", &Module{Path: "foo", Version: "v1.0.0", Replace: &Replace{Path: "../foo"}}, true},
		{"remote module", &Module{Path: "golang.org/x/crypto", Version: "v0.17.0"}, false},
		{"remote replace", &Module{Path: "foo", Version: "v1.0.0", Replace: &Replace{Path: "bar", Version: "v2.0.0"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.IsLocal(); got != tt.want {
				t.Errorf("IsLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestModuleModKey(t *testing.T) {
	m := &Module{Path: "golang.org/x/crypto", Version: "v0.17.0"}
	if got := m.ModKey(); got != "golang.org/x/crypto@v0.17.0" {
		t.Errorf("ModKey() = %q", got)
	}

	// With replace
	m2 := &Module{Path: "old/mod", Version: "v1.0.0", Replace: &Replace{Path: "new/mod", Version: "v2.0.0"}}
	if got := m2.ModKey(); got != "old/mod@v2.0.0" {
		t.Errorf("ModKey() with replace = %q", got)
	}
}

func TestCollectModules(t *testing.T) {
	pkgs := []Pkg{
		{ImportPath: "golang.org/x/crypto/ssh", Module: &Module{Path: "golang.org/x/crypto", Version: "v0.17.0"}},
		{ImportPath: "golang.org/x/crypto/chacha20", Module: &Module{Path: "golang.org/x/crypto", Version: "v0.17.0"}},
		{ImportPath: "github.com/foo/bar", Module: &Module{Path: "github.com/foo/bar", Version: "v1.0.0"}},
		{ImportPath: "mymod/internal", Module: &Module{Path: "mymod", Main: true}},
	}
	mods := CollectModules(pkgs)
	if len(mods) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(mods))
	}
	// Should be sorted
	if mods[0].Key != "github.com/foo/bar@v1.0.0" {
		t.Errorf("mods[0] = %q", mods[0].Key)
	}
	if mods[1].Key != "golang.org/x/crypto@v0.17.0" {
		t.Errorf("mods[1] = %q", mods[1].Key)
	}
}

func TestModInfoReplaced(t *testing.T) {
	m := ModInfo{Key: "old/mod@v1.0.0", FetchPath: "new/mod", Version: "v1.0.0"}
	if got := m.Replaced(); got != "new/mod" {
		t.Errorf("Replaced() = %q, want %q", got, "new/mod")
	}

	m2 := ModInfo{Key: "golang.org/x/crypto@v0.17.0", FetchPath: "golang.org/x/crypto", Version: "v0.17.0"}
	if got := m2.Replaced(); got != "" {
		t.Errorf("Replaced() = %q, want empty", got)
	}
}
