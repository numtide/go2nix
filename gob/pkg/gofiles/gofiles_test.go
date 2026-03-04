package gofiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ListFiles(dir, "")
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
	ctx := BuildContext("integration,debug")
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
