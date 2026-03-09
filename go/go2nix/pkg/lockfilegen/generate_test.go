package lockfilegen

import (
	"testing"

	"github.com/numtide/go2nix/pkg/golist"
)

func TestIsLocal(t *testing.T) {
	tests := []struct {
		name string
		mod  *golist.Module
		want bool
	}{
		{"nil module", nil, true},
		{"main module", &golist.Module{Path: "example.com/app", Main: true}, true},
		{"local replace (no version)", &golist.Module{
			Path:    "example.com/lib",
			Version: "v1.0.0",
			Replace: &golist.Replace{Path: "../lib"},
		}, true},
		{"remote replace (has version)", &golist.Module{
			Path:    "example.com/lib",
			Version: "v1.0.0",
			Replace: &golist.Replace{Path: "example.com/lib-fork", Version: "v2.0.0"},
		}, false},
		{"regular third-party", &golist.Module{
			Path:    "github.com/foo/bar",
			Version: "v1.2.3",
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.IsLocal(); got != tt.want {
				t.Errorf("IsLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReplaced(t *testing.T) {
	tests := []struct {
		name string
		mod  golist.ModInfo
		want string
	}{
		{
			"no replacement",
			golist.ModInfo{Key: "github.com/foo/bar@v1.0.0", FetchPath: "github.com/foo/bar", Version: "v1.0.0"},
			"",
		},
		{
			"replaced with different path",
			golist.ModInfo{Key: "github.com/foo/bar@v1.0.0", FetchPath: "github.com/foo/bar-fork", Version: "v1.0.0"},
			"github.com/foo/bar-fork",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mod.Replaced(); got != tt.want {
				t.Errorf("Replaced() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCollectModules(t *testing.T) {
	pkgs := []golist.Pkg{
		{
			ImportPath: "github.com/foo/bar/pkg1",
			Module:     &golist.Module{Path: "github.com/foo/bar", Version: "v1.0.0"},
		},
		{
			// Same module, different package — should be deduplicated.
			ImportPath: "github.com/foo/bar/pkg2",
			Module:     &golist.Module{Path: "github.com/foo/bar", Version: "v1.0.0"},
		},
		{
			ImportPath: "github.com/baz/qux",
			Module:     &golist.Module{Path: "github.com/baz/qux", Version: "v2.3.4"},
		},
		{
			// Remote replace — fetchPath should be the replacement.
			ImportPath: "github.com/old/mod/sub",
			Module: &golist.Module{
				Path:    "github.com/old/mod",
				Version: "v0.1.0",
				Replace: &golist.Replace{Path: "github.com/new/mod", Version: "v0.1.0"},
			},
		},
		{
			// No version (local module) — should be skipped.
			ImportPath: "example.com/local/pkg",
			Module:     &golist.Module{Path: "example.com/local", Main: true},
		},
		{
			// Nil module — should be skipped.
			ImportPath: "builtin/thing",
		},
	}

	mods := golist.CollectModules(pkgs)

	if len(mods) != 3 {
		t.Fatalf("expected 3 modules, got %d: %+v", len(mods), mods)
	}

	// Should be sorted by key.
	if mods[0].Key != "github.com/baz/qux@v2.3.4" {
		t.Errorf("mods[0].Key = %q, want github.com/baz/qux@v2.3.4", mods[0].Key)
	}
	if mods[1].Key != "github.com/foo/bar@v1.0.0" {
		t.Errorf("mods[1].Key = %q, want github.com/foo/bar@v1.0.0", mods[1].Key)
	}
	if mods[2].Key != "github.com/old/mod@v0.1.0" {
		t.Errorf("mods[2].Key = %q, want github.com/old/mod@v0.1.0", mods[2].Key)
	}

	// The replaced module should have the fork as fetchPath.
	if mods[2].FetchPath != "github.com/new/mod" {
		t.Errorf("mods[2].FetchPath = %q, want github.com/new/mod", mods[2].FetchPath)
	}
	if mods[2].Replaced() != "github.com/new/mod" {
		t.Errorf("mods[2].Replaced() = %q, want github.com/new/mod", mods[2].Replaced())
	}

	// Non-replaced modules should have matching fetchPath.
	if mods[0].FetchPath != "github.com/baz/qux" {
		t.Errorf("mods[0].FetchPath = %q, want github.com/baz/qux", mods[0].FetchPath)
	}
	if mods[0].Replaced() != "" {
		t.Errorf("mods[0].Replaced() = %q, want empty", mods[0].Replaced())
	}
}

func TestCollectModules_Empty(t *testing.T) {
	mods := golist.CollectModules(nil)
	if len(mods) != 0 {
		t.Errorf("expected empty, got %d modules", len(mods))
	}
}
