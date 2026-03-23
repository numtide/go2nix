// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/numtide/go2nix/pkg/gofiles"
)

// Options configures a package compilation.
type Options struct {
	ImportPath string // Go import path (e.g., "github.com/foo/bar")
	PFlag      string // -p flag for go tool compile (defaults to ImportPath)
	SrcDir     string // directory containing source files
	Output     string // output .a archive path
	ImportCfg  string // path to importcfg file
	TrimPath   string // path prefix to trim (defaults to $NIX_BUILD_TOP)
	Tags        string   // comma-separated build tags
	GCFlagsList []string // extra flags for go tool compile
	GoVersion  string // Go language version for -lang flag (e.g., "1.21"); auto-detected from go.mod if empty
	PGOProfile string // path to pprof CPU profile for PGO; empty disables PGO
	GoFiles    []string          // explicit Go files to compile (bypasses ListFiles discovery; paths relative to SrcDir)
	EmbedCfg   *gofiles.EmbedCfg // explicit embed config (used with GoFiles to pass pre-resolved embed metadata)

	// Resolved once by CompilePackage; avoids repeated go env subprocesses.
	goroot        string
	goos          string
	goarch        string
	asmArchDefs   []string // arch-specific -D flags for go tool asm
	trimRewrite   string   // computed -trimpath rewrite in "old=>new;old2=>new2" format
	concurrency   int      // -c=N backend concurrency for go tool compile
	pgoPreprofile string   // path to preprocessed PGO profile (output of go tool preprofile)
}

// CompileGoPackage compiles a single Go package (pure Go, assembly, or cgo).
func CompileGoPackage(opts Options) error {
	if opts.PFlag == "" {
		opts.PFlag = opts.ImportPath
	}
	if opts.TrimPath == "" {
		opts.TrimPath = os.Getenv("NIX_BUILD_TOP")
	}

	// Resolve Go environment once to avoid repeated subprocesses.
	var err error
	opts.goroot, err = goRoot()
	if err != nil {
		return err
	}
	opts.goos, opts.goarch = goEnv()
	opts.asmArchDefs = asmArchDefines(opts.goarch)

	// Auto-detect Go language version from go.mod if not set explicitly,
	// matching cmd/go's -lang=goX.Y behavior (see gc.go).
	if opts.GoVersion == "" {
		opts.GoVersion = findGoVersion(opts.SrcDir)
	}

	// Compute backend concurrency for go tool compile,
	// matching cmd/go behavior (gc.go:181-239).
	opts.concurrency = gcBackendConcurrency()

	// Compute -trimpath rewrite string matching cmd/go behavior (gc.go:243-310).
	// Rewrites source dir to import path so debug info shows
	// "github.com/foo/bar/file.go" instead of "/nix/store/xxx/file.go".
	// Strips build temp dir (TrimPath) for generated files (go_asm.h, symabis, etc.).
	opts.trimRewrite = opts.SrcDir + "=>" + opts.ImportPath + ";" + opts.TrimPath + "=>"

	// Preprocess PGO profile if provided. go tool preprofile converts a
	// pprof CPU profile into a text format (GO PREPROFILE V1) that the
	// compiler reads more efficiently, avoiding redundant parsing across
	// packages. Ships with Go 1.22+.
	if opts.PGOProfile != "" {
		uid := strings.ReplaceAll(opts.ImportPath, "/", "_")
		opts.pgoPreprofile = filepath.Join(opts.TrimPath, "pgoprofile_"+uid)
		if err := runIn("", "go", "tool", "preprofile", "-o", opts.pgoPreprofile, opts.PGOProfile); err != nil {
			return fmt.Errorf("preprofile: %w", err)
		}
	}

	slog.Debug("compile-package", "import-path", opts.ImportPath, "src", opts.SrcDir)

	var files gofiles.PkgFiles
	if len(opts.GoFiles) > 0 {
		// Explicit file list (used by test runner for _test.go files that
		// go/build.ImportDir would place in TestGoFiles, not GoFiles).
		files.GoFiles = opts.GoFiles
		files.EmbedCfg = opts.EmbedCfg
	} else {
		var err error
		files, err = gofiles.ListFiles(opts.SrcDir, opts.Tags)
		if err != nil {
			return fmt.Errorf("listing files: %w", err)
		}
	}

	if len(files.GoFiles) == 0 && len(files.CgoFiles) == 0 {
		return fmt.Errorf("no Go files found in %s (package %s)", opts.SrcDir, opts.ImportPath)
	}

	// Create output directory.
	if err := os.MkdirAll(filepath.Dir(opts.Output), 0o755); err != nil {
		return err
	}

	// Write embedcfg if needed.
	var embedFlag string
	if files.EmbedCfg != nil {
		uid := strings.ReplaceAll(opts.ImportPath, "/", "_")
		embedPath := filepath.Join(opts.TrimPath, "embedcfg_"+uid+".json")
		f, err := os.Create(embedPath)
		if err != nil {
			return fmt.Errorf("creating embedcfg: %w", err)
		}
		err = json.NewEncoder(f).Encode(files.EmbedCfg)
		if closeErr := f.Close(); err != nil {
			return fmt.Errorf("writing embedcfg: %w", err)
		} else if closeErr != nil {
			return fmt.Errorf("closing embedcfg: %w", closeErr)
		}
		embedFlag = "-embedcfg=" + embedPath
	}

	if len(files.CgoFiles) > 0 {
		return compileCgo(opts, files, embedFlag)
	}
	if len(files.SFiles) > 0 {
		return compileWithAsm(opts, files, embedFlag)
	}
	return compileGo(opts, files, embedFlag)
}
