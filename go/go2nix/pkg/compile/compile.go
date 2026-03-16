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
	Tags       string // comma-separated build tags
	GCFlags    string // extra flags for go tool compile (space-separated, e.g. "-race")
	GoVersion  string // Go language version for -lang flag (e.g., "1.21"); auto-detected from go.mod if empty

	// Resolved once by CompilePackage; avoids repeated go env subprocesses.
	goroot      string
	goos        string
	goarch      string
	asmArchDefs []string // arch-specific -D flags for go tool asm
	trimRewrite string   // computed -trimpath rewrite in "old=>new;old2=>new2" format
	concurrency int      // -c=N backend concurrency for go tool compile
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

	slog.Debug("compile-package", "import-path", opts.ImportPath, "src", opts.SrcDir)

	files, err := gofiles.ListFiles(opts.SrcDir, opts.Tags)
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
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
		if err := json.NewEncoder(f).Encode(files.EmbedCfg); err != nil {
			f.Close()
			return fmt.Errorf("writing embedcfg: %w", err)
		}
		f.Close()
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
