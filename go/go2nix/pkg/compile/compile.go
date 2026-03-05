// Package compile implements Go package compilation, replacing the shell
// pipeline in nix/compile.nix with a single Go process.
package compile

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
}

// CompilePackage compiles a single Go package (pure Go, assembly, or cgo).
func CompilePackage(opts Options) error {
	if opts.PFlag == "" {
		opts.PFlag = opts.ImportPath
	}
	if opts.TrimPath == "" {
		opts.TrimPath = os.Getenv("NIX_BUILD_TOP")
	}

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
	return compilePureGo(opts, files, embedFlag)
}

func compilePureGo(opts Options, files gofiles.PkgFiles, embedFlag string) error {
	args := []string{
		"tool", "compile",
		"-importcfg", opts.ImportCfg,
		"-p", opts.PFlag,
		"-trimpath=" + opts.TrimPath,
		"-pack",
		"-o", opts.Output,
	}
	args = append(args, extraGCFlags(opts)...)
	if embedFlag != "" {
		args = append(args, embedFlag)
	}
	args = append(args, files.GoFiles...)

	return runIn(opts.SrcDir, "go", args...)
}

// --- helpers ---

func extraGCFlags(opts Options) []string {
	if opts.GCFlags == "" {
		return nil
	}
	return strings.Fields(opts.GCFlags)
}

func runIn(dir, name string, args ...string) error {
	slog.Debug("exec", "cmd", name, "args", args, "dir", dir)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func run(name string, args ...string) error {
	slog.Debug("exec", "cmd", name, "args", args)
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func goRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func goEnv() (goos, goarch string) {
	goos = os.Getenv("GOOS")
	if goos == "" {
		out, _ := exec.Command("go", "env", "GOOS").Output()
		goos = strings.TrimSpace(string(out))
	}
	goarch = os.Getenv("GOARCH")
	if goarch == "" {
		out, _ := exec.Command("go", "env", "GOARCH").Output()
		goarch = strings.TrimSpace(string(out))
	}
	return
}

func extractPackageName(goFile string) string {
	data, err := os.ReadFile(goFile)
	if err != nil {
		return "main"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if after, ok := strings.CutPrefix(line, "package "); ok {
			return strings.TrimSpace(after)
		}
	}
	return "main"
}
