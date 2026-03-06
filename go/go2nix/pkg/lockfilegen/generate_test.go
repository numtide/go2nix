package lockfilegen

import (
	"testing"
)

func TestIsLocal(t *testing.T) {
	tests := []struct {
		name string
		mod  *goListModule
		want bool
	}{
		{"nil module", nil, true},
		{"main module", &goListModule{Path: "example.com/app", Main: true}, true},
		{"local replace (no version)", &goListModule{
			Path:    "example.com/lib",
			Version: "v1.0.0",
			Replace: &goListReplace{Path: "../lib"},
		}, true},
		{"remote replace (has version)", &goListModule{
			Path:    "example.com/lib",
			Version: "v1.0.0",
			Replace: &goListReplace{Path: "example.com/lib-fork", Version: "v2.0.0"},
		}, false},
		{"regular third-party", &goListModule{
			Path:    "github.com/foo/bar",
			Version: "v1.2.3",
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.isLocal(); got != tt.want {
				t.Errorf("isLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReplaced(t *testing.T) {
	tests := []struct {
		name string
		mod  modInfo
		want string
	}{
		{
			"no replacement",
			modInfo{key: "github.com/foo/bar@v1.0.0", fetchPath: "github.com/foo/bar", version: "v1.0.0"},
			"",
		},
		{
			"replaced with different path",
			modInfo{key: "github.com/foo/bar@v1.0.0", fetchPath: "github.com/foo/bar-fork", version: "v1.0.0"},
			"github.com/foo/bar-fork",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.replaced(); got != tt.want {
				t.Errorf("replaced() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCollectModulesFromPackages(t *testing.T) {
	pkgs := []goListPkg{
		{
			ImportPath: "github.com/foo/bar/pkg1",
			Module:     &goListModule{Path: "github.com/foo/bar", Version: "v1.0.0"},
		},
		{
			// Same module, different package — should be deduplicated.
			ImportPath: "github.com/foo/bar/pkg2",
			Module:     &goListModule{Path: "github.com/foo/bar", Version: "v1.0.0"},
		},
		{
			ImportPath: "github.com/baz/qux",
			Module:     &goListModule{Path: "github.com/baz/qux", Version: "v2.3.4"},
		},
		{
			// Remote replace — fetchPath should be the replacement.
			ImportPath: "github.com/old/mod/sub",
			Module: &goListModule{
				Path:    "github.com/old/mod",
				Version: "v0.1.0",
				Replace: &goListReplace{Path: "github.com/new/mod", Version: "v0.1.0"},
			},
		},
		{
			// No version (local module) — should be skipped.
			ImportPath: "example.com/local/pkg",
			Module:     &goListModule{Path: "example.com/local", Main: true},
		},
		{
			// Nil module — should be skipped.
			ImportPath: "builtin/thing",
		},
	}

	mods := collectModulesFromPackages(pkgs)

	if len(mods) != 3 {
		t.Fatalf("expected 3 modules, got %d: %+v", len(mods), mods)
	}

	// Should be sorted by key.
	if mods[0].key != "github.com/baz/qux@v2.3.4" {
		t.Errorf("mods[0].key = %q, want github.com/baz/qux@v2.3.4", mods[0].key)
	}
	if mods[1].key != "github.com/foo/bar@v1.0.0" {
		t.Errorf("mods[1].key = %q, want github.com/foo/bar@v1.0.0", mods[1].key)
	}
	if mods[2].key != "github.com/old/mod@v0.1.0" {
		t.Errorf("mods[2].key = %q, want github.com/old/mod@v0.1.0", mods[2].key)
	}

	// The replaced module should have the fork as fetchPath.
	if mods[2].fetchPath != "github.com/new/mod" {
		t.Errorf("mods[2].fetchPath = %q, want github.com/new/mod", mods[2].fetchPath)
	}
	if mods[2].replaced() != "github.com/new/mod" {
		t.Errorf("mods[2].replaced() = %q, want github.com/new/mod", mods[2].replaced())
	}

	// Non-replaced modules should have matching fetchPath.
	if mods[0].fetchPath != "github.com/baz/qux" {
		t.Errorf("mods[0].fetchPath = %q, want github.com/baz/qux", mods[0].fetchPath)
	}
	if mods[0].replaced() != "" {
		t.Errorf("mods[0].replaced() = %q, want empty", mods[0].replaced())
	}
}

func TestCollectModulesFromPackages_Empty(t *testing.T) {
	mods := collectModulesFromPackages(nil)
	if len(mods) != 0 {
		t.Errorf("expected empty, got %d modules", len(mods))
	}
}
