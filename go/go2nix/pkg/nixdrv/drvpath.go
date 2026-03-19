package nixdrv

import (
	"encoding/hex"
	"fmt"
	"sort"

	gonixdrv "github.com/numtide/go2nix/internal/gonix/derivation"
	"github.com/numtide/go2nix/internal/gonix/nixhash"
	"github.com/numtide/go2nix/internal/gonix/storepath"
)

// DrvPath computes the .drv store path for this derivation in-process,
// without calling `nix derivation add`. Uses go-nix's ATerm serialization
// and hashing, which implements the same algorithm Nix uses internally.
//
// This enables computing all .drv paths up front (in topo order), then
// registering derivations with the store in parallel — eliminating the
// sequential subprocess bottleneck.
func (d *Derivation) DrvPath() (*storepath.StorePath, error) {
	gnd, err := d.toGoNixDerivation()
	if err != nil {
		return nil, fmt.Errorf("converting derivation %q: %w", d.name, err)
	}

	// For FODs, nix derivation add computes output paths and fills them
	// into Output.Path and env[outputName] before writing the .drv file.
	// The .drv path is the store path of the .drv file content, so we must
	// match this by filling in the same values.
	if isFOD(gnd) {
		outputPaths, err := gnd.CalculateOutputPaths(nil)
		if err != nil {
			return nil, fmt.Errorf("computing FOD output paths for %q: %w", d.name, err)
		}
		for name, path := range outputPaths {
			gnd.Outputs[name].Path = path
			gnd.Env[name] = path
		}
	}

	path, err := gnd.DrvPath()
	if err != nil {
		return nil, fmt.Errorf("computing drv path for %q: %w", d.name, err)
	}

	return storepath.FromAbsolutePath(path)
}

// isFOD returns true if the derivation is a fixed-output derivation
// (single output named "out" with a known hash).
func isFOD(d *gonixdrv.Derivation) bool {
	if len(d.Outputs) != 1 {
		return false
	}
	o, ok := d.Outputs["out"]
	return ok && o.HashAlgorithm != "" && o.Hash != ""
}

// toGoNixDerivation converts from our v4-JSON-oriented Derivation to
// go-nix's ATerm-oriented Derivation. Only supports CA floating and FOD
// outputs (the types used by go2nix's resolve flow).
func (d *Derivation) toGoNixDerivation() (*gonixdrv.Derivation, error) {
	// Convert outputs
	outputs := make(map[string]*gonixdrv.Output, len(d.outputs))
	for name, o := range d.outputs {
		gno, err := convertOutput(o)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", name, err)
		}
		outputs[name] = gno
	}

	// Convert input derivations (full paths → sorted output names)
	inputDrvs := make(map[string][]string, len(d.inputDrvs))
	for path, drv := range d.inputDrvs {
		outs := make([]string, len(drv.Outputs))
		copy(outs, drv.Outputs)
		sort.Strings(outs)
		inputDrvs[path] = outs
	}

	// Sort input sources
	inputSrcs := make([]string, len(d.inputSrcs))
	copy(inputSrcs, d.inputSrcs)
	sort.Strings(inputSrcs)

	// Build env — v4 JSON does NOT include "name" in env; it's a top-level
	// field. We use SetName() on the go-nix Derivation to provide the name
	// without injecting it into the env map (which would change the ATerm hash).
	env := make(map[string]string, len(d.env))
	for k, v := range d.env {
		env[k] = v
	}

	gnd := &gonixdrv.Derivation{
		Outputs:          outputs,
		InputSources:     inputSrcs,
		InputDerivations: inputDrvs,
		Platform:         d.system,
		Builder:          d.builder,
		Arguments:        d.args,
		Env:              env,
	}
	gnd.SetName(d.name)

	return gnd, nil
}

// convertOutput maps a v4-JSON output to ATerm format.
//
// v4 JSON CA floating: {HashAlgo: "sha256", Method: "nar"}  → ATerm: ("out","","r:sha256","")
// v4 JSON FOD:         {Method: "nar", Hash: "sha256-..."}  → ATerm: ("out","","r:sha256","hexhash")
func convertOutput(o *Output) (*gonixdrv.Output, error) {
	gno := &gonixdrv.Output{}

	// Determine the ATerm hashAlgorithm prefix from the method.
	methodPrefix := ""
	switch o.Method {
	case "nar":
		methodPrefix = "r:"
	case "flat":
		methodPrefix = ""
	case "text":
		methodPrefix = ""
	case "":
		// Input-addressed output (has path, no method)
		gno.Path = o.Path
		return gno, nil
	default:
		return nil, fmt.Errorf("unsupported output method: %q", o.Method)
	}

	if o.HashAlgo != "" {
		// CA floating output: method + hashAlgo, no hash
		gno.HashAlgorithm = methodPrefix + o.HashAlgo
	} else if o.Hash != "" {
		// FOD: method + SRI hash (contains algo)
		algo, hexHash, err := parseSRIHash(o.Hash)
		if err != nil {
			return nil, err
		}
		gno.HashAlgorithm = methodPrefix + algo
		gno.Hash = hexHash
	}

	return gno, nil
}

// parseSRIHash parses an SRI hash (e.g., "sha256-BASE64==") into the
// algorithm name and hex-encoded digest, which is the format ATerm uses.
func parseSRIHash(sri string) (algo string, hexHash string, err error) {
	h, err := nixhash.ParseAny(sri, nil)
	if err != nil {
		return "", "", fmt.Errorf("parsing SRI hash %q: %w", sri, err)
	}
	return h.Algo().String(), hex.EncodeToString(h.Digest()), nil
}
