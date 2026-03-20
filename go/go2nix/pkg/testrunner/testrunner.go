// Package testrunner compiles and runs tests for local Go packages.
// It is invoked during the checkPhase of DAG mode builds, after
// compile-packages has already built all library archives.
package testrunner

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/compile"
	"github.com/numtide/go2nix/pkg/localpkgs"
	"github.com/numtide/go2nix/pkg/testmain"
)

// Options configures the test runner.
type Options struct {
	ModuleRoot string // path to module root (containing go.mod)
	ImportCfg  string // path to importcfg (from build phase, has all packages)
	LocalDir   string // directory with compiled local .a files
	TrimPath   string // path prefix to trim
	Tags       string // comma-separated build tags
	GCFlags    string // extra compiler flags
	CheckFlags string // flags to pass to test binaries (e.g., "-v -count=1")
}

// Run discovers testable packages and runs their tests.
func Run(opts Options) error {
	pkgs, err := localpkgs.ListLocalPackages(opts.ModuleRoot, opts.Tags)
	if err != nil {
		return fmt.Errorf("listing local packages: %w", err)
	}

	// Filter to packages with test files
	var testable []*localpkgs.LocalPkg
	for _, p := range pkgs {
		if len(p.TestGoFiles) > 0 || len(p.XTestGoFiles) > 0 {
			testable = append(testable, p)
		}
	}

	if len(testable) == 0 {
		slog.Info("no testable packages found")
		return nil
	}
	slog.Info("testable packages", "count", len(testable))

	for _, p := range testable {
		if err := runPackageTests(opts, p); err != nil {
			return fmt.Errorf("testing %s: %w", p.ImportPath, err)
		}
	}
	return nil
}

func runPackageTests(opts Options, pkg *localpkgs.LocalPkg) error {
	slog.Info("testing", "pkg", pkg.ImportPath)

	testDir := filepath.Join(opts.TrimPath, "test-"+sanitize(pkg.ImportPath))
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		return err
	}

	// We need a separate importcfg for test compilation that overrides
	// the package under test with the internal test archive.
	testImportCfg := filepath.Join(testDir, "importcfg")
	if err := copyFile(opts.ImportCfg, testImportCfg); err != nil {
		return fmt.Errorf("copying importcfg: %w", err)
	}

	// Step 1: Compile internal test archive (replaces library archive)
	// This includes the library's GoFiles + internal _test.go files,
	// compiled with the same import path as the original package.
	internalArchive := filepath.Join(testDir, "internal.a")
	if len(pkg.TestGoFiles) > 0 {
		// Create a merged source directory with library + test files
		mergedDir := filepath.Join(testDir, "src-internal")
		if err := os.MkdirAll(mergedDir, 0o755); err != nil {
			return err
		}
		// Symlink all source files from the package directory
		allFiles := append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...)
		allFiles = append(allFiles, pkg.SFiles...)
		allFiles = append(allFiles, pkg.CFiles...)
		allFiles = append(allFiles, pkg.CXXFiles...)
		allFiles = append(allFiles, pkg.HFiles...)
		allFiles = append(allFiles, pkg.TestGoFiles...)
		for _, f := range allFiles {
			src := filepath.Join(pkg.SrcDir, f)
			dst := filepath.Join(mergedDir, f)
			if err := os.Symlink(src, dst); err != nil && !os.IsExist(err) {
				return fmt.Errorf("symlinking %s: %w", f, err)
			}
		}
		// Also symlink embed files if any
		for _, f := range pkg.EmbedFiles {
			src := filepath.Join(pkg.SrcDir, f)
			dst := filepath.Join(mergedDir, f)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("creating embed dir for %s: %w", f, err)
			}
			if err := os.Symlink(src, dst); err != nil && !os.IsExist(err) {
				return fmt.Errorf("symlinking embed %s: %w", f, err)
			}
		}

		if err := compile.CompileGoPackage(compile.Options{
			ImportPath: pkg.ImportPath,
			SrcDir:     mergedDir,
			Output:     internalArchive,
			ImportCfg:  testImportCfg,
			TrimPath:   opts.TrimPath,
			Tags:       opts.Tags,
			GCFlags:    opts.GCFlags,
		}); err != nil {
			return fmt.Errorf("compiling internal test: %w", err)
		}
	} else {
		// No internal tests — use the library archive as-is
		libArchive := filepath.Join(opts.LocalDir, pkg.ImportPath+".a")
		if err := copyFile(libArchive, internalArchive); err != nil {
			// Library might be a command package (no .a), skip
			internalArchive = ""
		}
	}

	// Override the package entry in importcfg to point to the test archive.
	// We rewrite the importcfg filtering out any existing entry for this
	// package to avoid duplicate packagefile directives.
	if internalArchive != "" {
		if err := overrideImportCfgEntry(testImportCfg, pkg.ImportPath, internalArchive); err != nil {
			return fmt.Errorf("overriding importcfg for %s: %w", pkg.ImportPath, err)
		}
	}

	// Step 2: Compile external test archive
	externalArchive := filepath.Join(testDir, "external.a")
	if len(pkg.XTestGoFiles) > 0 {
		xtestDir := filepath.Join(testDir, "src-xtest")
		if err := os.MkdirAll(xtestDir, 0o755); err != nil {
			return err
		}
		for _, f := range pkg.XTestGoFiles {
			src := filepath.Join(pkg.SrcDir, f)
			dst := filepath.Join(xtestDir, f)
			if err := os.Symlink(src, dst); err != nil && !os.IsExist(err) {
				return fmt.Errorf("symlinking %s: %w", f, err)
			}
		}

		if err := compile.CompileGoPackage(compile.Options{
			ImportPath: pkg.ImportPath + "_test",
			SrcDir:     xtestDir,
			Output:     externalArchive,
			ImportCfg:  testImportCfg,
			TrimPath:   opts.TrimPath,
			Tags:       opts.Tags,
			GCFlags:    opts.GCFlags,
		}); err != nil {
			return fmt.Errorf("compiling external test: %w", err)
		}

		// Add external test to importcfg
		f, err := os.OpenFile(testImportCfg, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := fmt.Fprintf(f, "packagefile %s=%s\n", pkg.ImportPath+"_test", externalArchive)
		if closeErr := f.Close(); writeErr != nil {
			return writeErr
		} else if closeErr != nil {
			return closeErr
		}
	}

	// Step 3: Generate test main
	testMainFile := filepath.Join(testDir, "_testmain.go")
	var testFiles, xtestFiles []string
	for _, f := range pkg.TestGoFiles {
		testFiles = append(testFiles, filepath.Join(pkg.SrcDir, f))
	}
	for _, f := range pkg.XTestGoFiles {
		xtestFiles = append(xtestFiles, filepath.Join(pkg.SrcDir, f))
	}
	src, err := testmain.Generate(testmain.Options{
		ImportPath:   pkg.ImportPath,
		TestGoFiles:  testFiles,
		XTestGoFiles: xtestFiles,
	})
	if err != nil {
		return fmt.Errorf("generating test main: %w", err)
	}
	if err := os.WriteFile(testMainFile, src, 0o644); err != nil {
		return err
	}

	// Step 4: Compile test main
	testMainArchive := filepath.Join(testDir, "testmain.a")
	if err := compile.CompileGoPackage(compile.Options{
		ImportPath: pkg.ImportPath + ".test",
		PFlag:      "main",
		SrcDir:     testDir,
		Output:     testMainArchive,
		ImportCfg:  testImportCfg,
		TrimPath:   opts.TrimPath,
		Tags:       opts.Tags,
		GCFlags:    opts.GCFlags,
	}); err != nil {
		return fmt.Errorf("compiling test main: %w", err)
	}

	// Step 5: Link test binary
	testBin := filepath.Join(testDir, "test.exe")
	linkImportCfg := testImportCfg + ".link"
	if err := copyFile(testImportCfg, linkImportCfg); err != nil {
		return err
	}

	// Use `go tool link` rather than invoking the linker binary directly.
	// The build phase invokes the linker directly to prevent GOROOT embedding
	// in production binaries, but test binaries are ephemeral so this doesn't
	// matter. Using `go tool link` matches how compile uses `go tool compile`.
	linkArgs := []string{
		"tool", "link",
		"-buildid=redacted",
		"-importcfg", linkImportCfg,
		"-o", testBin,
		testMainArchive,
	}
	cmd := exec.Command("go", linkArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("linking test binary: %w", err)
	}

	// Step 6: Run tests
	var testArgs []string
	if opts.CheckFlags != "" {
		testArgs = strings.Fields(opts.CheckFlags)
	}
	testCmd := exec.Command(testBin, testArgs...)
	testCmd.Dir = pkg.SrcDir
	testCmd.Stdout = os.Stdout
	testCmd.Stderr = os.Stderr
	if err := testCmd.Run(); err != nil {
		return fmt.Errorf("tests failed: %w", err)
	}

	slog.Info("tests passed", "pkg", pkg.ImportPath)
	return nil
}

// overrideImportCfgEntry rewrites the importcfg file, replacing (or adding)
// the packagefile entry for importPath with the given archive path.
// This avoids duplicate packagefile directives which have undefined behavior.
func overrideImportCfgEntry(importCfgPath, importPath, archivePath string) error {
	data, err := os.ReadFile(importCfgPath)
	if err != nil {
		return err
	}

	prefix := "packagefile " + importPath + "="
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			lines = append(lines, line)
		}
	}
	lines = append(lines, fmt.Sprintf("packagefile %s=%s", importPath, archivePath))

	return os.WriteFile(importCfgPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", ".", "_").Replace(s)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
