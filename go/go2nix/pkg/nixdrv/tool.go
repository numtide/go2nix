package nixdrv

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/nix-community/go-nix/pkg/storepath"
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

	out, err := t.run(bytes.NewReader(jsonData), "derivation", "add")
	if err != nil {
		return nil, fmt.Errorf("nix derivation add for %q: %w\nJSON: %s", drv.name, err, jsonData)
	}
	return storepath.FromAbsolutePath(out)
}

// Build runs `nix build <installables> --no-link --print-out-paths` and returns output paths.
func (t *NixTool) Build(installables ...string) ([]*storepath.StorePath, error) {
	args := append([]string{"build", "--no-link", "--print-out-paths"}, installables...)
	out, err := t.run(nil, args...)
	if err != nil {
		return nil, fmt.Errorf("nix build %v: %w", installables, err)
	}

	var paths []*storepath.StorePath
	for _, line := range strings.Split(out, "\n") {
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
	out, err := t.run(nil, "store", "add", "--name", name, path)
	if err != nil {
		return nil, fmt.Errorf("nix store add %q: %w", name, err)
	}
	return storepath.FromAbsolutePath(out)
}

// run executes a nix subcommand, returning trimmed stdout.
// If stdin is non-nil it is piped to the process.
// On failure the error includes stderr.
func (t *NixTool) run(stdin io.Reader, args ...string) (string, error) {
	cmdArgs := make([]string, len(t.ExtraArgs)+len(args))
	copy(cmdArgs, t.ExtraArgs)
	copy(cmdArgs[len(t.ExtraArgs):], args)

	cmd := exec.Command(t.NixBin, cmdArgs...)
	cmd.Stdin = stdin

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w\nstderr: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}
