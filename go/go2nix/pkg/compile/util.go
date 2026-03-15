package compile

import (
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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

// goEnvVar queries a single Go env variable, checking os.Getenv first.
func goEnvVar(key string) string {
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
	return goEnvVar("GOOS"), goEnvVar("GOARCH")
}

func extraGCFlags(opts Options) []string {
	if opts.GCFlags == "" {
		return nil
	}
	return strings.Fields(opts.GCFlags)
}

// asmArchDefines returns architecture-specific -D flags for go tool asm,
// matching cmd/go/internal/work.asmArgs.
func asmArchDefines(goarch string) []string {
	var defs []string
	switch goarch {
	case "386":
		v := goEnvVar("GO386")
		if v != "" {
			defs = append(defs, "-D", "GO386_"+v)
		}
	case "amd64":
		v := goEnvVar("GOAMD64")
		if v != "" {
			defs = append(defs, "-D", "GOAMD64_"+v)
		}
	case "arm":
		v := goEnvVar("GOARM")
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
		v := goEnvVar("GOARM64")
		if strings.Contains(v, "lse") {
			defs = append(defs, "-D", "GOARM64_LSE")
		}
	case "mips", "mipsle":
		v := goEnvVar("GOMIPS")
		if v != "" {
			defs = append(defs, "-D", "GOMIPS_"+v)
		}
	case "mips64", "mips64le":
		v := goEnvVar("GOMIPS64")
		if v != "" {
			defs = append(defs, "-D", "GOMIPS64_"+v)
		}
	case "ppc64", "ppc64le":
		v := goEnvVar("GOPPC64")
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
		v := goEnvVar("GORISCV64")
		if v != "" {
			defs = append(defs, "-D", "GORISCV64_"+v)
		}
	}
	return defs
}

// DefaultBuildMode returns the default -buildmode for go tool link,
// matching cmd/go's platform.DefaultPIE logic (without race consideration).
// Returns "pie" for platforms where Go defaults to PIE, "exe" otherwise.
func DefaultBuildMode(goos, goarch string) string {
	switch goos {
	case "android", "ios":
		return "pie"
	case "windows", "darwin":
		return "pie"
	}
	return "exe"
}

func extractPackageName(goFile string) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goFile, nil, parser.PackageClauseOnly)
	if err != nil || f.Name == nil {
		return "main"
	}
	return f.Name.Name
}
