package compile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/numtide/go2nix/pkg/gofiles"
)

// compileTinyArchive writes a one-file package into dir and returns the
// archive `go tool compile -pack` produces for it.
func compileTinyArchive(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not found")
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "p.a")
	cmd := exec.Command("go", "tool", "compile", "-p", "example.com/p", "-pack", "-o", archive, "p.go")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go tool compile: %v\n%s", err, out)
	}
	return archive
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMember creates an archive member source with an exact mode
// (os.WriteFile alone is subject to the umask).
func writeMember(t *testing.T, path string, size int, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestPackAppend_MatchesGoToolPack(t *testing.T) {
	dir := t.TempDir()
	base := compileTinyArchive(t, dir)

	members := []string{
		filepath.Join(dir, "odd_example.com_p.o"), // odd size: padded; name over 16 bytes: truncated
		filepath.Join(dir, "even.o"),
		filepath.Join(dir, "blob_linux_amd64.syso"),
		filepath.Join(dir, "dynimportfail"), // cgo's empty marker member
	}
	for i, size := range []int{7, 8, 101, 0} {
		writeMember(t, members[i], size, 0o644)
	}

	want := filepath.Join(dir, "want.a")
	copyFile(t, base, want)
	cmd := exec.Command("go", append([]string{"tool", "pack", "r", want}, members...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go tool pack unavailable: %v\n%s", err, out)
	}

	got := filepath.Join(dir, "got.a")
	copyFile(t, base, got)
	// Two calls, as compileCgo appends _cgo_flags separately.
	if err := packAppend(got, members[:2]); err != nil {
		t.Fatal(err)
	}
	if err := packAppend(got, members[2:]); err != nil {
		t.Fatal(err)
	}

	wantData, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotData, wantData) {
		t.Errorf("archive differs from go tool pack r:\n got: %q\nwant: %q", gotData, wantData)
	}
}

// The entry layout, without cmd/pack: name cut or padded to 16 bytes,
// mtime, uid and gid 0, decimal size, a pad byte only after an odd size,
// and mode 644 whatever the file has. A .syso in the Nix store is 0444;
// `go build` (cmd/go's packInternal) still writes 644, where cmd/pack
// would copy 444.
func TestPackAppend_Format(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "p.a")
	if err := os.WriteFile(archive, []byte("!<arch>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	syso := filepath.Join(dir, "blob.syso")
	writeMember(t, syso, 3, 0o444)
	long := filepath.Join(dir, "add_amd64_example.com_p.o")
	writeMember(t, long, 4, 0o644)
	empty := filepath.Join(dir, "dynimportfail")
	writeMember(t, empty, 0, 0o644)

	if err := packAppend(archive, []string{syso, long, empty}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	want := "!<arch>\n" +
		"blob.syso       0           0     0     644     3         `\n" + "xxx\x00" +
		"add_amd64_exampl0           0     0     644     4         `\n" + "xxxx" +
		"dynimportfail   0           0     0     644     0         `\n"
	if string(got) != want {
		t.Errorf("archive = %q, want %q", got, want)
	}
}

func TestPackAppend_MissingArchive(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "a.o")
	writeMember(t, obj, 1, 0o644)
	if err := packAppend(filepath.Join(dir, "missing.a"), []string{obj}); err == nil {
		t.Fatal("expected an error for a missing archive, got nil")
	}
}

// cmd/pack is not among the tools shipped in GOTOOLDIR (as of Go 1.26),
// so `go tool pack` has cmd/go build it first, which needs the build
// cache. With the cache disabled an assembly package compiles only if
// nothing has to be built.
func TestCompileGoPackage_AsmNeedsNoBuildCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not found")
	}
	t.Setenv("GOCACHE", "off")

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "p.go"), []byte("package p\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "empty.s"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	importcfg := filepath.Join(work, "importcfg")
	if err := os.WriteFile(importcfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(work, "out", "p.a")

	err := CompileGoPackage(Options{
		ImportPath: "example.com/p",
		SrcDir:     src,
		Output:     output,
		ImportCfg:  importcfg,
		TrimPath:   work,
		Files:      &gofiles.PkgFiles{GoFiles: []string{"p.go"}, SFiles: []string{"empty.s"}},
	})
	if err != nil {
		t.Fatalf("CompileGoPackage with GOCACHE=off: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	// empty_example.com_p.o, cut to the 16-byte ar name field.
	if !strings.Contains(string(data), "empty_example.co0           0     0     644     ") {
		t.Errorf("archive has no entry for the assembly object:\n%q", data)
	}
}

// The same for the compileGo path, which appends .syso files.
func TestCompileGoPackage_SysoNeedsNoBuildCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not found")
	}
	t.Setenv("GOCACHE", "off")

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "p.go"), []byte("package p\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMember(t, filepath.Join(src, "blob.syso"), 3, 0o444)
	work := t.TempDir()
	importcfg := filepath.Join(work, "importcfg")
	if err := os.WriteFile(importcfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(work, "out", "p.a")

	err := CompileGoPackage(Options{
		ImportPath: "example.com/p",
		SrcDir:     src,
		Output:     output,
		ImportCfg:  importcfg,
		TrimPath:   work,
		Files:      &gofiles.PkgFiles{GoFiles: []string{"p.go"}, SysoFiles: []string{"blob.syso"}},
	})
	if err != nil {
		t.Fatalf("CompileGoPackage with GOCACHE=off: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "blob.syso       0           0     0     644     3         `\nxxx\x00") {
		t.Errorf("archive does not end with the .syso entry:\n%q", data)
	}
}
