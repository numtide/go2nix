package derivation

import (
	"fmt"

	"github.com/numtide/go2nix/internal/gonix/storepath"
)

// Derivation describes all data in a .drv, which canonically is expressed in ATerm format.
// Nix requires some stronger properties w.r.t. order of elements, so we can internally use
// maps for some of the fields, and convert to the canonical representation when encoding back
// to ATerm format.
// The field names (and order of fields) also match the json structure
// that the `nix show-derivation /path/to.drv` is using,
// even though this might change in the future.
type Derivation struct {
	// name holds the derivation name when it is not stored in env["name"].
	// This occurs with structured attrs (where name is in the __json blob)
	// and with Nix v4 JSON derivations (where name is a top-level field,
	// not an env variable). Use SetName() to set this field.
	name string

	// Outputs are always lexicographically sorted by their name (key in this map)
	Outputs map[string]*Output `json:"outputs"`

	// InputSources are always lexicographically sorted.
	InputSources []string `json:"inputSrcs"`

	// InputDerivations are always lexicographically sorted by their path (key in this map)
	// the []string returns the output names (out, …) of this input derivation that are used.
	InputDerivations map[string][]string `json:"inputDrvs"`

	Platform string `json:"system"`

	Builder string `json:"builder"`

	Arguments []string `json:"args"`

	// Env must be lexicographically sorted by their key.
	Env map[string]string `json:"env"`
}

func (d *Derivation) Validate() error {
	numberOfOutputs := len(d.Outputs)

	if numberOfOutputs == 0 {
		return fmt.Errorf("at least one output must be defined")
	}

	for outputName, output := range d.Outputs {
		if outputName == "" {
			return fmt.Errorf("empty output name")
		}

		// TODO: are there more restrictions on output names?

		// we encountered a fixed-output output
		// In these derivations, there may be only one output,
		// which needs to be called out
		if output.HashAlgorithm != "" {
			if numberOfOutputs != 1 {
				return fmt.Errorf("encountered fixed-output, but there's more than 1 output in total")
			}

			if outputName != "out" {
				return fmt.Errorf("the fixed-output output name must be called 'out'")
			}

			// we confirmed above there's only one output, so we're done with the loop
			break
		}

		err := output.Validate()
		if err != nil {
			return fmt.Errorf("error validating output '%s': %w", outputName, err)
		}
	}

	for inputDerivationPath := range d.InputDerivations {
		err := storepath.Validate(inputDerivationPath)
		if err != nil {
			return err
		}

		outputNames := d.InputDerivations[inputDerivationPath]
		if len(outputNames) == 0 {
			return fmt.Errorf("output names list for '%s' empty", inputDerivationPath)
		}

		for i, o := range outputNames {
			if i > 0 && o < outputNames[i-1] {
				return fmt.Errorf("invalid input derivation output order: %s < %s", o, outputNames[i-1])
			}

			if o == "" {
				return fmt.Errorf("Output name entry for '%s' empty", inputDerivationPath)
			}
		}
	}

	for i, is := range d.InputSources {
		err := storepath.Validate(is)
		if err != nil {
			return fmt.Errorf("error validating input source '%s': %w", is, err)
		}

		if i > 0 && is < d.InputSources[i-1] {
			return fmt.Errorf("invalid input source order: %s < %s", is, d.InputSources[i-1])
		}
	}

	if d.Platform == "" {
		return fmt.Errorf("required attribute 'platform' missing")
	}

	if d.Builder == "" {
		return fmt.Errorf("required attribute 'builder' missing")
	}

	// The derivation name must be available either via env["name"],
	// the explicit name field (v4 JSON / structured attrs), or __json.
	hasName := d.name != ""

	for k := range d.Env {
		if k == "" {
			return fmt.Errorf("empty environment variable key")
		}

		if k == "name" {
			hasName = true
		}

		// Structured attrs
		if k == "__json" {
			hasName = d.name != ""
		}
	}

	if !hasName {
		return fmt.Errorf("derivation name not found (set env 'name' or use SetName)")
	}

	return nil
}

// SetName sets the derivation name explicitly. Use this for derivations
// where the name is not stored in env["name"] (e.g., Nix v4 JSON format).
func (d *Derivation) SetName(name string) {
	d.name = name
}

// Name returns the derivation name. It checks, in order:
//  1. The explicit name field (set via SetName or structured attrs parsing)
//  2. The env["name"] variable (traditional ATerm derivations)
func (d *Derivation) Name() string {
	if d.name != "" {
		return d.name
	}

	name, ok := d.Env["name"]
	if ok {
		return name
	}

	return ""
}
