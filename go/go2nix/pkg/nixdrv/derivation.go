package nixdrv

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/nix-community/go-nix/pkg/storepath"
)

// Derivation represents a Nix derivation in the format accepted by
// `nix derivation add` (JSON on stdin).
type Derivation struct {
	name      string
	system    string
	builder   string
	args      []string
	env       map[string]string
	inputDrvs map[string]*InputDrv
	inputSrcs map[string]struct{}
	outputs   map[string]*Output
}

// InputDrv represents an input derivation reference.
type InputDrv struct {
	Outputs        []string             `json:"outputs"`
	DynamicOutputs map[string]*InputDrv `json:"dynamicOutputs"`
}

// Output represents a derivation output specification.
//
// Nix parses outputs by exact key-set matching:
//   - FOD (CAFixed):    {"method", "hash"}       — 2 keys only
//   - CA floating:      {"method", "hashAlgo"}   — 2 keys only
//   - Input-addressed:  {"path"}                 — 1 key only
//   - Deferred:         {}                       — empty object
//
// Using a single struct with omitempty ensures only the relevant keys are emitted.
type Output struct {
	HashAlgo string `json:"hashAlgo,omitempty"`
	Method   string `json:"method,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Path     string `json:"path,omitempty"`
}

// NewDerivation creates a new derivation with the given name, system, and builder.
func NewDerivation(name, system, builder string) *Derivation {
	return &Derivation{
		name:      name,
		system:    system,
		builder:   builder,
		args:      []string{},
		env:       make(map[string]string),
		inputDrvs: make(map[string]*InputDrv),
		inputSrcs: make(map[string]struct{}),
		outputs:   make(map[string]*Output),
	}
}

// AddArg appends a builder argument.
func (d *Derivation) AddArg(arg string) *Derivation {
	d.args = append(d.args, arg)
	return d
}

// Env returns a copy of the derivation's environment variables.
func (d *Derivation) Env() map[string]string {
	copy := make(map[string]string, len(d.env))
	for k, v := range d.env {
		copy[k] = v
	}
	return copy
}

// SetEnv sets an environment variable.
func (d *Derivation) SetEnv(key, value string) *Derivation {
	d.env[key] = value
	return d
}

// AddCAOutput adds a content-addressed output (no pre-known hash).
func (d *Derivation) AddCAOutput(name, hashAlgo, method string) *Derivation {
	d.outputs[name] = &Output{
		HashAlgo: hashAlgo,
		Method:   method,
	}
	return d
}

// AddFODOutput adds a fixed-output derivation output (with pre-known hash).
// Nix 2.34+ expects exactly {"method", "hash"} for FOD outputs.
// The hash must be in SRI format (e.g. "sha256-abc123==").
func (d *Derivation) AddFODOutput(name, method, hash string) *Derivation {
	d.outputs[name] = &Output{
		Method: method,
		Hash:   hash,
	}
	return d
}

// AddInputDrv adds an input derivation dependency.
func (d *Derivation) AddInputDrv(drvPath string, outputs ...string) *Derivation {
	if existing, ok := d.inputDrvs[drvPath]; ok {
		existing.Outputs = mergeUnique(existing.Outputs, outputs)
	} else {
		d.inputDrvs[drvPath] = &InputDrv{
			Outputs:        outputs,
			DynamicOutputs: make(map[string]*InputDrv),
		}
	}
	return d
}

// AddInputSrc adds an input source path.
func (d *Derivation) AddInputSrc(path string) *Derivation {
	d.inputSrcs[path] = struct{}{}
	return d
}

// ToJSON serializes the derivation to JSON matching the `nix derivation add` format.
// All maps are serialized with sorted keys.
func (d *Derivation) ToJSON() ([]byte, error) {
	return json.Marshal(d.toSerializable())
}

// derivationJSON is the JSON-serializable form matching Nix 2.34 v4 format.
type derivationJSON struct {
	Name    string             `json:"name"`
	Version int                `json:"version"`
	Outputs sortedMap[*Output] `json:"outputs"`
	Inputs  inputsJSON         `json:"inputs"`
	System  string             `json:"system"`
	Builder string             `json:"builder"`
	Args    []string           `json:"args"`
	Env     sortedMap[string]  `json:"env"`
}

// inputsJSON represents the nested inputs format: { srcs: [...], drvs: {...} }
type inputsJSON struct {
	Srcs sortedSlice          `json:"srcs"`
	Drvs sortedMap[*InputDrv] `json:"drvs"`
}

func (d *Derivation) toSerializable() derivationJSON {
	// v4 format uses store basenames (without /nix/store/ prefix)
	// for inputs.srcs and inputs.drvs keys.
	srcs := make([]string, 0, len(d.inputSrcs))
	for s := range d.inputSrcs {
		srcs = append(srcs, storeBaseName(s))
	}

	drvs := make(map[string]*InputDrv, len(d.inputDrvs))
	for k, v := range d.inputDrvs {
		drvs[storeBaseName(k)] = v
	}

	return derivationJSON{
		Name:    d.name,
		Version: 4,
		Outputs: sortedMap[*Output]{m: d.outputs},
		Inputs: inputsJSON{
			Srcs: sortedSlice(srcs),
			Drvs: sortedMap[*InputDrv]{m: drvs},
		},
		System:  d.system,
		Builder: d.builder,
		Args:    d.args,
		Env:     sortedMap[string]{m: d.env},
	}
}

// storeBaseName strips the /nix/store/ prefix from a store path,
// returning just the hash-name part (e.g. "abc123-foo-1.0").
func storeBaseName(path string) string {
	return strings.TrimPrefix(path, storepath.StoreDir+"/")
}

// sortedMap serializes a map with sorted keys.
type sortedMap[V any] struct {
	m map[string]V
}

func (s sortedMap[V]) MarshalJSON() ([]byte, error) {
	if len(s.m) == 0 {
		return []byte("{}"), nil
	}

	buf := []byte("{")
	for i, k := range slices.Sorted(maps.Keys(s.m)) {
		if i > 0 {
			buf = append(buf, ',')
		}
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		valJSON, err := json.Marshal(s.m[k])
		if err != nil {
			return nil, err
		}
		buf = append(buf, keyJSON...)
		buf = append(buf, ':')
		buf = append(buf, valJSON...)
	}
	buf = append(buf, '}')
	return buf, nil
}

// sortedSlice serializes a string slice in sorted order.
type sortedSlice []string

func (s sortedSlice) MarshalJSON() ([]byte, error) {
	return json.Marshal(slices.Sorted(slices.Values(s)))
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			a = append(a, s)
			seen[s] = true
		}
	}
	return a
}
