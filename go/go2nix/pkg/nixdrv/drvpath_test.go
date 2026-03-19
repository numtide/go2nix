package nixdrv

import (
	"testing"

	gonixdrv "github.com/nix-community/go-nix/pkg/derivation"
	"github.com/nix-community/go-nix/pkg/storepath"
)

func TestDrvPathCAFloating(t *testing.T) {
	// Build a CA floating derivation (the kind go2nix uses for package builds)
	drv := NewDerivation("go-compile-example.com-pkg", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddArg("-c")
	drv.AddArg("echo hello > $out")
	drv.SetEnv("PATH", "/nix/store/d1pzgj1pj3nk97vhm5x6n8szy4w3xhx7-coreutils/bin")
	drv.AddCAOutput("out", "sha256", "nar")
	drv.AddInputSrc("/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-src")

	sp, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("DrvPath: %v", err)
	}

	// Verify it's a valid store path with .drv suffix
	if sp.Name == "" {
		t.Error("store path name is empty")
	}
	if len(sp.Digest) != 20 {
		t.Errorf("digest length = %d, want 20", len(sp.Digest))
	}
	abs := sp.Absolute()
	if abs[:len(storepath.StoreDir)] != storepath.StoreDir {
		t.Errorf("path doesn't start with store dir: %s", abs)
	}
	t.Logf("computed drv path: %s", abs)
}

func TestDrvPathFOD(t *testing.T) {
	// Build a FOD derivation (the kind used for module fetches)
	drv := NewDerivation("gomod-github.com-foo-bar-v1.0.0", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddArg("-c")
	drv.AddArg("cp -r $src $out")
	// Use a real-looking SRI hash (sha256 of "test")
	drv.AddFODOutput("out", "nar", "sha256-n4bQgYhMfWWaL+qgxVrQFaO/TxsrC4Is0V1sFbDwCgg=")

	sp, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("DrvPath: %v", err)
	}

	abs := sp.Absolute()
	if abs[:len(storepath.StoreDir)] != storepath.StoreDir {
		t.Errorf("path doesn't start with store dir: %s", abs)
	}
	t.Logf("computed FOD drv path: %s", abs)
}

func TestDrvPathMatchesGoNix(t *testing.T) {
	// Construct the same derivation via both our API and go-nix directly,
	// and verify they produce the same .drv path.
	name := "test-match"
	system := "x86_64-linux"
	builder := "/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash"
	args := []string{"-c", "echo ok > $out"}
	env := map[string]string{
		"PATH": "/nix/store/d1pzgj1pj3nk97vhm5x6n8szy4w3xhx7-coreutils/bin",
	}
	inputSrc := "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-src"

	// Our derivation
	drv := NewDerivation(name, system, builder)
	for _, a := range args {
		drv.AddArg(a)
	}
	for k, v := range env {
		drv.SetEnv(k, v)
	}
	drv.AddCAOutput("out", "sha256", "nar")
	drv.AddInputSrc(inputSrc)

	ourPath, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("our DrvPath: %v", err)
	}

	// go-nix derivation (ATerm format, directly)
	gnEnv := make(map[string]string, len(env)+1)
	for k, v := range env {
		gnEnv[k] = v
	}
	gnEnv["name"] = name

	gnDrv := &gonixdrv.Derivation{
		Outputs: map[string]*gonixdrv.Output{
			"out": {HashAlgorithm: "r:sha256"},
		},
		InputSources:     []string{inputSrc},
		InputDerivations: map[string][]string{},
		Platform:         system,
		Builder:          builder,
		Arguments:        args,
		Env:              gnEnv,
	}

	gnPathStr, err := gnDrv.DrvPath()
	if err != nil {
		t.Fatalf("go-nix DrvPath: %v", err)
	}
	gnSP, err := storepath.FromAbsolutePath(gnPathStr)
	if err != nil {
		t.Fatalf("parsing go-nix path: %v", err)
	}

	if ourPath.Absolute() != gnSP.Absolute() {
		t.Errorf("paths differ:\n  ours:   %s\n  go-nix: %s", ourPath.Absolute(), gnSP.Absolute())
	}
}

func TestDrvPathFODMatchesGoNix(t *testing.T) {
	// Verify FOD .drv path matches go-nix when output paths are filled in.
	// This is the critical test: nix derivation add computes output paths for
	// FODs and includes them in the .drv, which changes the ATerm hash.
	name := "gomod-test-v1.0.0"
	system := "x86_64-linux"
	builder := "/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash"
	sriHash := "sha256-n4bQgYhMfWWaL+qgxVrQFaO/TxsrC4Is0V1sFbDwCgg="
	hexHash := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	// Our derivation
	drv := NewDerivation(name, system, builder)
	drv.AddArg("-c")
	drv.AddArg("cp -r $src $out")
	drv.AddFODOutput("out", "nar", sriHash)
	drv.SetEnv("out", "")

	ourPath, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("our DrvPath: %v", err)
	}

	// go-nix derivation with output paths computed manually
	gnDrv := &gonixdrv.Derivation{
		Outputs: map[string]*gonixdrv.Output{
			"out": {HashAlgorithm: "r:sha256", Hash: hexHash},
		},
		InputSources:     []string{},
		InputDerivations: map[string][]string{},
		Platform:         system,
		Builder:          builder,
		Arguments:        []string{"-c", "cp -r $src $out"},
		Env: map[string]string{
			"name": name,
			"out":  "",
		},
	}

	// Compute output paths (fills in Output.Path)
	outputPaths, err := gnDrv.CalculateOutputPaths(nil)
	if err != nil {
		t.Fatalf("CalculateOutputPaths: %v", err)
	}
	for oname, opath := range outputPaths {
		gnDrv.Outputs[oname].Path = opath
		gnDrv.Env[oname] = opath
	}

	gnPathStr, err := gnDrv.DrvPath()
	if err != nil {
		t.Fatalf("go-nix DrvPath: %v", err)
	}
	gnSP, err := storepath.FromAbsolutePath(gnPathStr)
	if err != nil {
		t.Fatalf("parsing go-nix path: %v", err)
	}

	if ourPath.Absolute() != gnSP.Absolute() {
		t.Errorf("FOD paths differ:\n  ours:   %s\n  go-nix: %s", ourPath.Absolute(), gnSP.Absolute())
	}
	t.Logf("FOD drv path (matched): %s", ourPath.Absolute())
}

func TestDrvPathWithInputDrvs(t *testing.T) {
	// Test that input derivation conversion is correct
	drv := NewDerivation("link-drv", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddArg("-c")
	drv.AddArg("echo linking")
	drv.AddCAOutput("out", "sha256", "nar")
	drv.AddInputDrv("/nix/store/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-dep1.drv", "out")
	drv.AddInputDrv("/nix/store/cccccccccccccccccccccccccccccccccc-dep2.drv", "out", "lib")

	sp, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("DrvPath: %v", err)
	}

	// Also build the go-nix equivalent and compare
	gnDrv := &gonixdrv.Derivation{
		Outputs: map[string]*gonixdrv.Output{
			"out": {HashAlgorithm: "r:sha256"},
		},
		InputSources: []string{},
		InputDerivations: map[string][]string{
			"/nix/store/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-dep1.drv": {"out"},
			"/nix/store/cccccccccccccccccccccccccccccccccc-dep2.drv": {"lib", "out"},
		},
		Platform:  "x86_64-linux",
		Builder:   "/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash",
		Arguments: []string{"-c", "echo linking"},
		Env: map[string]string{
			"name": "link-drv",
		},
	}

	gnPathStr, err := gnDrv.DrvPath()
	if err != nil {
		t.Fatalf("go-nix DrvPath: %v", err)
	}
	gnSP, err := storepath.FromAbsolutePath(gnPathStr)
	if err != nil {
		t.Fatalf("parsing go-nix path: %v", err)
	}

	if sp.Absolute() != gnSP.Absolute() {
		t.Errorf("paths differ:\n  ours:   %s\n  go-nix: %s", sp.Absolute(), gnSP.Absolute())
	}
}

func TestParseSRIHash(t *testing.T) {
	tests := []struct {
		sri     string
		algo    string
		hexHash string
	}{
		{
			// sha256 of "test"
			sri:     "sha256-n4bQgYhMfWWaL+qgxVrQFaO/TxsrC4Is0V1sFbDwCgg=",
			algo:    "sha256",
			hexHash: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		},
		{
			// sha512 of "test"
			sri:     "sha512-7iaw3Ur350mqGo7jwQrpkj9hiYB3Lkc/iBml1JQODbJ6wYX4oOHV+E+IvIh/1nsUNzLDBMxfqa2Ob1f1ACio/w==",
			algo:    "sha512",
			hexHash: "ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff",
		},
	}

	for _, tc := range tests {
		algo, hexHash, err := parseSRIHash(tc.sri)
		if err != nil {
			t.Errorf("parseSRIHash(%q): %v", tc.sri, err)
			continue
		}
		if algo != tc.algo {
			t.Errorf("algo = %q, want %q", algo, tc.algo)
		}
		if hexHash != tc.hexHash {
			t.Errorf("hexHash = %q, want %q", hexHash, tc.hexHash)
		}
	}
}

func TestDrvPathInputAddressed(t *testing.T) {
	// Test input-addressed output (has path, no method)
	drv := NewDerivation("ia-test", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddArg("-c")
	drv.AddArg("echo test")
	drv.outputs["out"] = &Output{Path: "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-ia-test"}

	sp, err := drv.DrvPath()
	if err != nil {
		t.Fatalf("DrvPath: %v", err)
	}
	if sp.Absolute() == "" {
		t.Error("empty store path")
	}
	t.Logf("input-addressed drv path: %s", sp.Absolute())
}
