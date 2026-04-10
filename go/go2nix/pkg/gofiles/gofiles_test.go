package gofiles

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ListFiles(dir, "", "")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files.GoFiles) != 1 || files.GoFiles[0] != "main.go" {
		t.Errorf("GoFiles = %v, want [main.go]", files.GoFiles)
	}
	if !files.IsCommand {
		t.Error("IsCommand = false, want true")
	}
}

func TestBuildContextTags(t *testing.T) {
	ctx := BuildContext("integration,debug", "")
	if len(ctx.BuildTags) != 2 {
		t.Errorf("BuildTags = %v, want [integration debug]", ctx.BuildTags)
	}
}

func TestNonNilSlice(t *testing.T) {
	got := nonNil(nil)
	if got == nil {
		t.Error("nonNil(nil) returned nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("nonNil(nil) = %v, want []", got)
	}
}

// writeFile is a test helper that creates a file with the given content,
// creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveEmbedCfg_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.txt"), "b")
	writeFile(t, filepath.Join(dir, "c.go"), "package x")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["*.txt"]
	slices.Sort(matched)
	if len(matched) != 2 || matched[0] != "a.txt" || matched[1] != "b.txt" {
		t.Errorf("Patterns[*.txt] = %v, want [a.txt b.txt]", matched)
	}
	if cfg.Files["a.txt"] != filepath.Join(dir, "a.txt") {
		t.Errorf("Files[a.txt] = %q, want %q", cfg.Files["a.txt"], filepath.Join(dir, "a.txt"))
	}
	if _, ok := cfg.Files["c.go"]; ok {
		t.Error("c.go should not be in Files")
	}
}

func TestResolveEmbedCfg_Directory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "index.html"), "<html>")
	writeFile(t, filepath.Join(dir, "static", "css", "style.css"), "body{}")

	cfg, err := ResolveEmbedCfg(dir, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["static"]
	slices.Sort(matched)
	want := []string{"static/css/style.css", "static/index.html"}
	if !slices.Equal(matched, want) {
		t.Errorf("Patterns[static] = %v, want %v", matched, want)
	}
	for _, f := range want {
		if cfg.Files[f] != filepath.Join(dir, f) {
			t.Errorf("Files[%s] = %q, want %q", f, cfg.Files[f], filepath.Join(dir, f))
		}
	}
}

func TestResolveEmbedCfg_HiddenFilesExcluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "visible.txt"), "ok")
	writeFile(t, filepath.Join(dir, "static", ".hidden"), "secret")
	writeFile(t, filepath.Join(dir, "static", "_private"), "secret")

	cfg, err := ResolveEmbedCfg(dir, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["static"]
	if len(matched) != 1 || matched[0] != "static/visible.txt" {
		t.Errorf("Patterns[static] = %v, want [static/visible.txt]", matched)
	}
}

func TestResolveEmbedCfg_AllPrefixIncludesHidden(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "visible.txt"), "ok")
	writeFile(t, filepath.Join(dir, "static", ".hidden"), "included")
	writeFile(t, filepath.Join(dir, "static", "_private"), "included")

	cfg, err := ResolveEmbedCfg(dir, []string{"all:static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["all:static"]
	slices.Sort(matched)
	want := []string{"static/.hidden", "static/_private", "static/visible.txt"}
	if !slices.Equal(matched, want) {
		t.Errorf("Patterns[all:static] = %v, want %v", matched, want)
	}
}

func TestResolveEmbedCfg_HiddenDirSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "static", "ok.txt"), "ok")
	writeFile(t, filepath.Join(dir, "static", ".git", "config"), "nope")
	writeFile(t, filepath.Join(dir, "static", "_build", "out.txt"), "nope")

	cfg, err := ResolveEmbedCfg(dir, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["static"]
	if len(matched) != 1 || matched[0] != "static/ok.txt" {
		t.Errorf("Patterns[static] = %v, want [static/ok.txt]", matched)
	}
}

func TestResolveEmbedCfg_MultiplePatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.sql"), "select 1")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt", "*.sql"})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Patterns) != 2 {
		t.Fatalf("expected 2 pattern entries, got %d", len(cfg.Patterns))
	}
	if cfg.Patterns["*.txt"][0] != "a.txt" {
		t.Errorf("Patterns[*.txt] = %v, want [a.txt]", cfg.Patterns["*.txt"])
	}
	if cfg.Patterns["*.sql"][0] != "b.sql" {
		t.Errorf("Patterns[*.sql] = %v, want [b.sql]", cfg.Patterns["*.sql"])
	}
	if len(cfg.Files) != 2 {
		t.Errorf("expected 2 Files entries, got %d", len(cfg.Files))
	}
}

func TestResolveEmbedCfg_NoMatches(t *testing.T) {
	dir := t.TempDir()

	_, err := ResolveEmbedCfg(dir, []string{"*.txt"})
	if err == nil {
		t.Fatal("expected error for pattern with no matches, got nil")
	}
	if !strings.Contains(err.Error(), "no matching files found") {
		t.Errorf("expected 'no matching files found' in error, got: %v", err)
	}
}

func TestMergeEmbedCfg(t *testing.T) {
	a := &EmbedCfg{
		Patterns: map[string][]string{"data.txt": {"data.txt"}},
		Files:    map[string]string{"data.txt": "/src/data.txt"},
	}
	b := &EmbedCfg{
		Patterns: map[string][]string{"testdata/*": {"testdata/x.txt"}},
		Files:    map[string]string{"testdata/x.txt": "/src/testdata/x.txt"},
	}

	t.Run("both nil", func(t *testing.T) {
		got, err := MergeEmbedCfg(nil, nil)
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("a nil", func(t *testing.T) {
		got, err := MergeEmbedCfg(nil, b)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, b) {
			t.Errorf("got %+v, want %+v", got, b)
		}
	})

	t.Run("b nil", func(t *testing.T) {
		got, err := MergeEmbedCfg(a, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, a) {
			t.Errorf("got %+v, want %+v", got, a)
		}
	})

	t.Run("disjoint union", func(t *testing.T) {
		got, err := MergeEmbedCfg(a, b)
		if err != nil {
			t.Fatal(err)
		}
		want := &EmbedCfg{
			Patterns: map[string][]string{
				"data.txt":   {"data.txt"},
				"testdata/*": {"testdata/x.txt"},
			},
			Files: map[string]string{
				"data.txt":       "/src/data.txt",
				"testdata/x.txt": "/src/testdata/x.txt",
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("same key same value ok", func(t *testing.T) {
		got, err := MergeEmbedCfg(a, a)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, a) {
			t.Errorf("got %+v, want %+v", got, a)
		}
	})

	t.Run("pattern conflict", func(t *testing.T) {
		c := &EmbedCfg{
			Patterns: map[string][]string{"data.txt": {"other.txt"}},
			Files:    map[string]string{},
		}
		_, err := MergeEmbedCfg(a, c)
		if err == nil || !strings.Contains(err.Error(), "resolves inconsistently") {
			t.Fatalf("got err=%v, want 'resolves inconsistently'", err)
		}
	})

	t.Run("file conflict", func(t *testing.T) {
		c := &EmbedCfg{
			Patterns: map[string][]string{},
			Files:    map[string]string{"data.txt": "/elsewhere/data.txt"},
		}
		_, err := MergeEmbedCfg(a, c)
		if err == nil || !strings.Contains(err.Error(), "maps to both") {
			t.Fatalf("got err=%v, want 'maps to both'", err)
		}
	})
}

func TestListFiles_LibraryPackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package lib\n\nvar X = 1\n")

	files, err := ListFiles(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if files.IsCommand {
		t.Error("IsCommand = true, want false for library package")
	}
	if len(files.GoFiles) != 1 || files.GoFiles[0] != "lib.go" {
		t.Errorf("GoFiles = %v, want [lib.go]", files.GoFiles)
	}
}

func TestListFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := ListFiles(dir, "", "")
	if err == nil {
		t.Fatal("expected error for directory with no Go files, got nil")
	}
}

func TestResolveEmbedCfg_InvalidPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")

	// "." is explicitly invalid for //go:embed
	_, err := ResolveEmbedCfg(dir, []string{"."})
	if err == nil {
		t.Fatal("expected error for invalid pattern '.', got nil")
	}
	if !strings.Contains(err.Error(), "invalid pattern syntax") {
		t.Errorf("expected 'invalid pattern syntax' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_ModuleBoundary(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory that contains its own go.mod — a module boundary.
	writeFile(t, filepath.Join(dir, "sub", "go.mod"), "module other\n")
	writeFile(t, filepath.Join(dir, "sub", "data.txt"), "data")

	_, err := ResolveEmbedCfg(dir, []string{"sub/data.txt"})
	if err == nil {
		t.Fatal("expected error for embedding across module boundary, got nil")
	}
	if !strings.Contains(err.Error(), "different module") {
		t.Errorf("expected 'different module' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_BadEmbedName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "config"), "bad")

	_, err := ResolveEmbedCfg(dir, []string{".git/config"})
	if err == nil {
		t.Fatal("expected error for bad embed name .git, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected 'invalid' in error, got: %v", err)
	}
}

func TestResolveEmbedCfg_DirModuleBoundary(t *testing.T) {
	dir := t.TempDir()
	// Directory embed should stop at module boundaries.
	writeFile(t, filepath.Join(dir, "data", "ok.txt"), "ok")
	writeFile(t, filepath.Join(dir, "data", "nested", "go.mod"), "module nested\n")
	writeFile(t, filepath.Join(dir, "data", "nested", "skip.txt"), "should be skipped")

	cfg, err := ResolveEmbedCfg(dir, []string{"data"})
	if err != nil {
		t.Fatal(err)
	}

	matched := cfg.Patterns["data"]
	// Should contain ok.txt but not nested/skip.txt (behind module boundary).
	if len(matched) != 1 || matched[0] != "data/ok.txt" {
		t.Errorf("Patterns[data] = %v, want [data/ok.txt]", matched)
	}
}

func TestResolveEmbedCfg_DirWithGlobMeta(t *testing.T) {
	// Package directory path contains glob metacharacters; they must be
	// matched literally, not as a glob, when resolving embed patterns.
	base := t.TempDir()
	dir := filepath.Join(base, "proj[v2]")
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.txt"), "b")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	matched := cfg.Patterns["*.txt"]
	slices.Sort(matched)
	if !slices.Equal(matched, []string{"a.txt", "b.txt"}) {
		t.Errorf("Patterns[*.txt] = %v, want [a.txt b.txt]", matched)
	}
	if cfg.Files["a.txt"] != filepath.Join(dir, "a.txt") {
		t.Errorf("Files[a.txt] = %q, want %q", cfg.Files["a.txt"], filepath.Join(dir, "a.txt"))
	}
}

func TestResolveEmbedCfg_Deduplication(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.txt"), "hello")
	writeFile(t, filepath.Join(dir, "other.txt"), "world")

	cfg, err := ResolveEmbedCfg(dir, []string{"*.txt", "readme.txt"})
	if err != nil {
		t.Fatal(err)
	}

	// Both patterns should resolve, but Files should not have duplicates.
	// *.txt matches both files; readme.txt matches one.
	if len(cfg.Files) != 2 {
		t.Errorf("expected 2 unique files, got %d: %v", len(cfg.Files), cfg.Files)
	}
	// The per-pattern lists should each be correct.
	if len(cfg.Patterns["*.txt"]) != 2 {
		t.Errorf("Patterns[*.txt] = %v, want 2 entries", cfg.Patterns["*.txt"])
	}
	if len(cfg.Patterns["readme.txt"]) != 1 {
		t.Errorf("Patterns[readme.txt] = %v, want 1 entry", cfg.Patterns["readme.txt"])
	}
}

func TestResolveEmbedCfg_SymlinkLeafFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.txt"), "content")
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	cfg, err := ResolveEmbedCfg(dir, []string{"link.txt"})
	if err != nil {
		t.Fatalf("ResolveEmbedCfg(link.txt) errored: %v; symlinked leaf files must be embeddable (golang/go#59924)", err)
	}
	if got := cfg.Patterns["link.txt"]; len(got) != 1 || got[0] != "link.txt" {
		t.Errorf("Patterns[link.txt] = %v, want [link.txt]", got)
	}
	if cfg.Files["link.txt"] != filepath.Join(dir, "link.txt") {
		t.Errorf("Files[link.txt] = %q, want %q", cfg.Files["link.txt"], filepath.Join(dir, "link.txt"))
	}
}

func TestResolveEmbedCfg_SymlinkToDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "realdir", "f.txt"), "x")
	if err := os.Symlink("realdir", filepath.Join(dir, "linkdir")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := ResolveEmbedCfg(dir, []string{"linkdir"})
	if err == nil {
		t.Fatal("ResolveEmbedCfg(linkdir) succeeded; want error: symlink-to-directory must be rejected (only leaf files are followed)")
	}
	if !strings.Contains(err.Error(), "cannot embed irregular file") {
		t.Errorf("got %q, want error containing %q", err, "cannot embed irregular file")
	}
}

func TestResolveEmbedCfg_DanglingSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("nonexistent", filepath.Join(dir, "dangling.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := ResolveEmbedCfg(dir, []string{"dangling.txt"})
	if err == nil {
		t.Fatal("ResolveEmbedCfg(dangling.txt) succeeded; want error: dangling symlinks must be rejected")
	}
}
