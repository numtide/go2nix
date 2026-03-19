package nixdrv

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/numtide/go2nix/internal/gonix/storepath"
)

// NixTool wraps the nix CLI for derivation operations.
type NixTool struct {
	NixBin    string   // path to nix binary
	ExtraArgs []string // e.g. ["--extra-experimental-features", "nix-command ca-derivations dynamic-derivations"]
}

// DerivationAdd pipes derivation JSON to `nix derivation add` and returns the .drv store path.
func (t *NixTool) DerivationAdd(drv *Derivation) (*storepath.StorePath, error) {
	jsonData, err := drv.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("serializing derivation %q: %w", drv.name, err)
	}

	args := append(t.baseArgs(), "derivation", "add")
	cmd := exec.Command(t.NixBin, args...)
	cmd.Stdin = bytes.NewReader(jsonData)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nix derivation add for %q failed: %w\nstderr: %s\nJSON: %s",
			drv.name, err, stderr.String(), string(jsonData))
	}

	drvPath := strings.TrimSpace(stdout.String())
	return storepath.FromAbsolutePath(drvPath)
}

// Build runs `nix build <installables> --no-link --print-out-paths` and returns output paths.
func (t *NixTool) Build(installables ...string) ([]*storepath.StorePath, error) {
	args := append(t.baseArgs(), "build")
	args = append(args, "--no-link", "--print-out-paths")
	args = append(args, installables...)

	cmd := exec.Command(t.NixBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nix build %v failed: %w\nstderr: %s",
			installables, err, stderr.String())
	}

	var paths []*storepath.StorePath
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sp, err := storepath.FromAbsolutePath(line)
		if err != nil {
			return nil, fmt.Errorf("parsing build output %q: %w", line, err)
		}
		paths = append(paths, sp)
	}
	return paths, nil
}

// StoreAdd runs `nix store add --name <name> <path>` and returns the store path.
func (t *NixTool) StoreAdd(name, path string) (*storepath.StorePath, error) {
	args := append(t.baseArgs(), "store", "add", "--name", name, path)
	cmd := exec.Command(t.NixBin, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nix store add %q failed: %w\nstderr: %s",
			name, err, stderr.String())
	}

	return storepath.FromAbsolutePath(strings.TrimSpace(stdout.String()))
}

func (t *NixTool) baseArgs() []string {
	args := make([]string, len(t.ExtraArgs))
	copy(args, t.ExtraArgs)
	return args
}
