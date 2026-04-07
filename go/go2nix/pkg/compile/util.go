package compile

import (
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
)

func runIn(dir, name string, args ...string) error {
	slog.Debug("exec", "cmd", name, "args", args, "dir", dir)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
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

// GoEnvVar queries a single Go env variable, checking os.Getenv first.
func GoEnvVar(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	out, _ := exec.Command("go", "env", key).Output()
	return strings.TrimSpace(string(out))
}

func goRoot() (string, error) {
	if v := os.Getenv("GOROOT"); v != "" {
		return v, nil
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func goEnv() (goos, goarch string) {
	return GoEnvVar("GOOS"), GoEnvVar("GOARCH")
}

func extraGCFlags(opts Options) []string {
	if len(opts.GCFlagsList) > 0 {
		return opts.GCFlagsList
	}
	return nil
}

// asmArchDefines returns architecture-specific -D flags for go tool asm,
// matching cmd/go/internal/work.asmArgs.
func asmArchDefines(goarch string) []string {
	var defs []string
	switch goarch {
	case "386":
		v := GoEnvVar("GO386")
		if v != "" {
			defs = append(defs, "-D", "GO386_"+v)
		}
	case "amd64":
		v := GoEnvVar("GOAMD64")
		if v != "" {
			defs = append(defs, "-D", "GOAMD64_"+v)
		}
	case "arm":
		v := GoEnvVar("GOARM")
		switch {
		case strings.Contains(v, "7"):
			defs = append(defs, "-D", "GOARM_7")
			fallthrough
		case strings.Contains(v, "6"):
			defs = append(defs, "-D", "GOARM_6")
			fallthrough
		default:
			defs = append(defs, "-D", "GOARM_5")
		}
	case "arm64":
		v := GoEnvVar("GOARM64")
		if strings.Contains(v, "lse") {
			defs = append(defs, "-D", "GOARM64_LSE")
		}
	case "mips", "mipsle":
		v := GoEnvVar("GOMIPS")
		if v != "" {
			defs = append(defs, "-D", "GOMIPS_"+v)
		}
	case "mips64", "mips64le":
		v := GoEnvVar("GOMIPS64")
		if v != "" {
			defs = append(defs, "-D", "GOMIPS64_"+v)
		}
	case "ppc64", "ppc64le":
		v := GoEnvVar("GOPPC64")
		switch v {
		case "power10":
			defs = append(defs, "-D", "GOPPC64_power10")
			fallthrough
		case "power9":
			defs = append(defs, "-D", "GOPPC64_power9")
			fallthrough
		default:
			defs = append(defs, "-D", "GOPPC64_power8")
		}
	case "riscv64":
		v := GoEnvVar("GORISCV64")
		if v != "" {
			defs = append(defs, "-D", "GORISCV64_"+v)
		}
	}
	return defs
}

// DefaultBuildMode returns the default -buildmode for go tool link,
// matching cmd/go's internal/platform.DefaultPIE logic (without the
// windows+race exception — see go.dev/cl/416174).
// The goarch parameter is unused today but kept to match the upstream
// DefaultPIE(goos, goarch, isRace) signature.
func DefaultBuildMode(goos, goarch string) string {
	switch goos {
	case "android", "ios", "windows", "darwin":
		return "pie"
	}
	return "exe"
}

// findGoVersion walks up from dir looking for go.mod and returns the
// Go language version (major.minor only), matching cmd/go's -lang behavior.
// Returns "" if no go.mod is found or it lacks a go directive.
func findGoVersion(dir string) string {
	for d := dir; ; {
		data, err := os.ReadFile(filepath.Join(d, "go.mod"))
		if err == nil {
			f, err := modfile.ParseLax(filepath.Join(d, "go.mod"), data, nil)
			if err == nil && f.Go != nil {
				return LangVersion(f.Go.Version)
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return ""
}

// ToolchainVersion returns the major.minor version of the Go toolchain
// that will be invoked for compilation, queried from `go env GOVERSION`
// (cached for the lifetime of the process). Used to override
// build.Context.ReleaseTags so file selection matches the target toolchain
// rather than the Go version that built this binary.
var ToolchainVersion = sync.OnceValue(func() string {
	v := GoEnvVar("GOVERSION")
	if v == "" {
		return ""
	}
	return LangVersion(strings.TrimPrefix(v, "go"))
})

// LangVersion strips the patch version from a Go version string,
// matching internal/gover.Lang: "1.21.3" → "1.21", "1.21" → "1.21".
func LangVersion(v string) string {
	major, rest, ok := strings.Cut(v, ".")
	if !ok {
		return v
	}
	minor, _, _ := strings.Cut(rest, ".")
	return major + "." + minor
}

// gcBackendConcurrency returns the -c flag value for the Go compiler,
// matching cmd/go/internal/work.gcBackendConcurrency (gc.go:181-239).
// Enables concurrent backend compilation within a single go tool compile
// invocation. Capped at min(4, GOMAXPROCS) since go2nix already handles
// package-level parallelism. Respects GO19CONCURRENTCOMPILATION env var.
func gcBackendConcurrency() int {
	switch os.Getenv("GO19CONCURRENTCOMPILATION") {
	case "0":
		return 1
	case "1":
		// Explicitly enabled, continue.
	case "":
		// Default: enabled.
	default:
		return 1
	}
	c := runtime.GOMAXPROCS(0)
	if c > 4 {
		c = 4
	}
	return c
}

func extractPackageName(goFile string) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goFile, nil, parser.PackageClauseOnly)
	if err != nil || f.Name == nil {
		return "main"
	}
	return f.Name.Name
}
