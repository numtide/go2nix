package nixdrv

import (
	"encoding/json"
	"testing"
)

func TestDerivationJSON(t *testing.T) {
	drv := NewDerivation("hello", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddArg("-c")
	drv.AddArg("echo Hello > $out")
	drv.SetEnv("PATH", "/nix/store/d1pzgj1pj3nk97vhm5x6n8szy4w3xhx7-coreutils/bin")
	drv.AddCAOutput("out", "", "")

	data, err := drv.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check required fields
	for _, field := range []string{"name", "system", "builder", "args", "env", "inputDrvs", "inputSrcs", "outputs"} {
		if _, ok := got[field]; !ok {
			t.Errorf("missing field %q", field)
		}
	}

	// Check name
	var name string
	json.Unmarshal(got["name"], &name)
	if name != "hello" {
		t.Errorf("name = %q, want %q", name, "hello")
	}
}

func TestDerivationCAOutput(t *testing.T) {
	drv := NewDerivation("ca-example", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddCAOutput("out", "sha256", "nar")

	data, err := drv.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	var got struct {
		Outputs map[string]struct {
			HashAlgo string `json:"hashAlgo"`
			Method   string `json:"method"`
			Hash     string `json:"hash"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := got.Outputs["out"]
	if out.HashAlgo != "sha256" {
		t.Errorf("hashAlgo = %q, want %q", out.HashAlgo, "sha256")
	}
	if out.Method != "nar" {
		t.Errorf("method = %q, want %q", out.Method, "nar")
	}
	if out.Hash != "" {
		t.Errorf("hash should be empty for CA output, got %q", out.Hash)
	}
}

func TestDerivationFODOutput(t *testing.T) {
	drv := NewDerivation("fod-example", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddFODOutput("out", "sha256", "nar", "sha256-abc123==")

	data, err := drv.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	var got struct {
		Outputs map[string]struct {
			HashAlgo string `json:"hashAlgo"`
			Method   string `json:"method"`
			Hash     string `json:"hash"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := got.Outputs["out"]
	if out.Hash != "sha256-abc123==" {
		t.Errorf("hash = %q, want %q", out.Hash, "sha256-abc123==")
	}
}

func TestDerivationSortedKeys(t *testing.T) {
	drv := NewDerivation("test", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.SetEnv("ZZZ", "last")
	drv.SetEnv("AAA", "first")
	drv.SetEnv("MMM", "middle")

	data, err := drv.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	// The env keys should be sorted: AAA, MMM, ZZZ
	s := string(data)
	aIdx := indexOf(s, `"AAA"`)
	mIdx := indexOf(s, `"MMM"`)
	zIdx := indexOf(s, `"ZZZ"`)

	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Fatalf("missing env keys in JSON: %s", s)
	}
	if !(aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("env keys not sorted: AAA@%d, MMM@%d, ZZZ@%d", aIdx, mIdx, zIdx)
	}
}

func TestDerivationInputDrv(t *testing.T) {
	drv := NewDerivation("test", "x86_64-linux",
		"/nix/store/w7jl0h7mwrrrcy2kgvk9c9h9142f1ca0-bash/bin/bash")
	drv.AddInputDrv("/nix/store/abc-dep1.drv", "out")
	drv.AddInputDrv("/nix/store/def-dep2.drv", "out")
	// duplicate should not create second entry
	drv.AddInputDrv("/nix/store/abc-dep1.drv", "out")

	data, err := drv.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	var got struct {
		InputDrvs map[string]struct {
			Outputs        []string                  `json:"outputs"`
			DynamicOutputs map[string]json.RawMessage `json:"dynamicOutputs"`
		} `json:"inputDrvs"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.InputDrvs) != 2 {
		t.Errorf("expected 2 input drvs, got %d", len(got.InputDrvs))
	}
	dep1 := got.InputDrvs["/nix/store/abc-dep1.drv"]
	if len(dep1.Outputs) != 1 || dep1.Outputs[0] != "out" {
		t.Errorf("dep1 outputs = %v, want [out]", dep1.Outputs)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
