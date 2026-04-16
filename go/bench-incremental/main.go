// bench-incremental measures rebuild time for go2nix incremental builds
// after touching a single file at different dep-graph depths and with
// different edit types (private vs exported symbols).
//
// Stores are client-only (NIX_REMOTE=local?root=...): no daemon, no
// socat, no sandbox-disable. The store roots under $TMPDIR are
// rooted-store sandboxes shared between tools so the comparison is fair.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type fixtureConfig struct {
	dir        string // subdirectory under tests/fixtures/
	modRoot    string
	subPackage string
	hasBazel   bool // fixture has Bazel BUILD files
	scenarios  []struct {
		name string
		path string // relative to fixture root
	}
}

var fixtures = map[string]fixtureConfig{
	"torture": {
		dir:        "torture-project",
		modRoot:    "app-full",
		subPackage: "./cmd/app-full",
		hasBazel:   true,
		scenarios: []struct {
			name string
			path string
		}{
			{"leaf", "app-full/cmd/app-full/main.go"},
			{"mid", "internal/aws/aws.go"},
			{"deep", "internal/common/common.go"},
		},
	},
	"light": {
		dir:        "light-project",
		modRoot:    "app",
		subPackage: "./cmd/app",
		scenarios: []struct {
			name string
			path string
		}{
			{"leaf", "app/cmd/app/main.go"},
			{"mid", "internal/handler/handler.go"},
			{"deep", "internal/core/core.go"},
		},
	},
}

const (
	touchMarker  = "// BENCHMARK_TOUCH"
	exprTemplate = `{ srcPath ? %s }:
let
  pkgs = import <nixpkgs> { system = "%s"; };
  go2nixLib = import %s/lib.nix {};
  goEnv = go2nixLib.mkGoEnv {
    go = pkgs.go_1_26;
    go2nix = import %s/packages/go2nix { inherit pkgs; };
    inherit (pkgs) callPackage;
  };
in
goEnv.buildGoApplication {
  src = srcPath;
  modRoot = "%s";
  goLock = "${srcPath}/%s/go2nix.toml";
  pname = "bench";
  version = "0.0.1";
  subPackages = [ "%s" ];
  doCheck = false;
  %s
}
`
	// Experimental builder (recursive-nix + dynamic-derivations + ca-derivations).
	// buildGoApplicationExperimental returns a CA wrapper drv whose .target
	// is the actual binary; wrap in runCommand so the existing instantiate
	// → realise flow in nixTool.Build works unchanged.
	dynExprTemplate = `{ srcPath ? %s }:
let
  pkgs = import <nixpkgs> { system = "%s"; };
  go2nixLib = import %s/lib.nix {};
  goEnv = go2nixLib.mkGoEnv {
    go = pkgs.go_1_26;
    go2nix = import %s/packages/go2nix { inherit pkgs; };
    nixPackage = pkgs.nixVersions.nix_2_34;
    inherit (pkgs) callPackage;
  };
  app = goEnv.buildGoApplicationExperimental {
    src = srcPath;
    modRoot = "%s";
    goLock = "${srcPath}/%s/go2nix.toml";
    pname = "bench";
    subPackages = [ "%s" ];
    %s
  };
in
pkgs.runCommand "bench-dynamic" { } "ln -s ${app.target} $out"
`
)

// Fixed symbol names + rotating values: each touch updates an existing
// symbol's body rather than declaring a fresh one. Models the common
// dev-loop edit ("changed a constant"). Declaring a new symbol each
// time would also work for `private` (Go's export data only encodes
// inittask presence, not body), but the symbol-name-in-export-data
// effect would muddy `exported`.
var touchTemplates = map[string]string{
	"private":  "var _benchTouch = uint64(%d) %s\n",
	"exported": "var BenchTouch = uint64(%d) %s\n",
}

type result struct {
	Scenario   string
	Tool       string
	Times      []float64
	EvalTimes  []float64
	BuildTimes []float64
	Builds     []int
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := mean(xs)
	var s float64
	for _, x := range xs {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(xs)-1))
}

func meanInt(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s int
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

func maxInt(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func minF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

// runCommand runs cmd with the given env overlay and returns wall time,
// stdout, stderr, and any error. The caller decides what a failure means.
func runCommand(name string, args []string, env map[string]string) (time.Duration, string, string, error) {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	t0 := time.Now()
	err := cmd.Run()
	elapsed := time.Since(t0)
	return elapsed, stdout.String(), stderr.String(), err
}

func touchFile(path, mode string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	tmpl, ok := touchTemplates[mode]
	if !ok {
		return fmt.Errorf("unknown touch mode: %s", mode)
	}
	line := fmt.Sprintf(tmpl, time.Now().UnixNano(), touchMarker)
	return os.WriteFile(path, []byte(string(content)+"\n"+line), 0o644)
}

// buildTool abstracts over build systems (Nix, Bazel, etc.) so the
// benchmark harness can compare them on equal footing.
type buildTool interface {
	Name() string
	Build(srcPath string) (buildResult, error)
	Cleanup() error
	// SkipOnFail reports whether a probe-build failure should drop this
	// tool with a notice (rather than aborting the whole run). Used for
	// nix-dynamic*, which needs recursive-nix in the build sandbox.
	SkipOnFail() bool
}

type nixTool struct {
	name        string
	nixpkgsPath string
	pluginPath  string
	gomodcache  string
	exprPath    string
	extraOpts   []string
	storeRoot   string // local store root (NIX_REMOTE=local?root=...)
	stderrTail  int    // bytes of stderr to keep in error messages
	skipOnFail  bool
}

func (t *nixTool) Name() string     { return t.name }
func (t *nixTool) Cleanup() error   { return nil }
func (t *nixTool) SkipOnFail() bool { return t.skipOnFail }

type buildResult struct {
	total     time.Duration
	evalTime  time.Duration
	buildTime time.Duration
	built     int
}

func (t *nixTool) Build(srcPath string) (buildResult, error) {
	baseArgs := []string{
		"-I", "nixpkgs=" + t.nixpkgsPath,
		"--option", "plugin-files", t.pluginPath,
	}
	baseArgs = append(baseArgs, t.extraOpts...)

	env := map[string]string{
		"GOMODCACHE": t.gomodcache,
	}
	if t.storeRoot != "" {
		env["NIX_REMOTE"] = "local?root=" + t.storeRoot
	}

	// Phase 1: eval (nix-instantiate produces .drv path)
	evalArgs := make([]string, len(baseArgs))
	copy(evalArgs, baseArgs)
	evalArgs = append(evalArgs, t.exprPath)
	if srcPath != "" {
		evalArgs = append(evalArgs, "--arg", "srcPath", srcPath)
	}
	evalElapsed, evalOut, evalErr, err := runCommand("nix-instantiate", evalArgs, env)
	if err != nil {
		tail := evalErr
		if len(tail) > t.stderrTail {
			tail = tail[len(tail)-t.stderrTail:]
		}
		return buildResult{}, fmt.Errorf("nix-instantiate failed: %w\n%s", err, tail)
	}
	drvPath := strings.TrimSpace(evalOut)
	if drvPath == "" {
		return buildResult{}, fmt.Errorf("nix-instantiate produced no drv path\n%s", evalErr)
	}

	// Phase 2: build (nix-store --realise)
	realiseArgs := []string{"--realise", drvPath}
	realiseArgs = append(realiseArgs, t.extraOpts...)
	// Pass plugin-files to nix-store too (needed for CA resolution)
	realiseArgs = append(realiseArgs, "--option", "plugin-files", t.pluginPath)
	buildElapsed, buildOut, buildErr, err := runCommand("nix-store", realiseArgs, env)
	if err != nil {
		tail := buildErr
		if len(tail) > t.stderrTail {
			tail = tail[len(tail)-t.stderrTail:]
		}
		return buildResult{}, fmt.Errorf("nix-store --realise failed: %w\n%s", err, tail)
	}
	built := strings.Count(buildOut+buildErr, "building '/nix/store/")
	return buildResult{
		total:     evalElapsed + buildElapsed,
		evalTime:  evalElapsed,
		buildTime: buildElapsed,
		built:     built,
	}, nil
}

// ---------------------------------------------------------------------------
// Bazel tool
// ---------------------------------------------------------------------------

type bazelTool struct {
	name       string
	workspace  string // path to Bazel workspace (the fixture copy)
	target     string // e.g. "//app-full/cmd/app-full"
	outputBase string // isolated Bazel output base directory
	stderrTail int
}

func (t *bazelTool) Name() string     { return t.name }
func (t *bazelTool) SkipOnFail() bool { return false }

// bazelProcessRe parses Bazel's action summary line, e.g.:
//
//	INFO: 42 processes: 10 internal, 32 linux-sandbox.
var bazelProcessRe = regexp.MustCompile(`INFO: (\d+) process`)

func (t *bazelTool) Build(_ string) (buildResult, error) {
	baseArgs := []string{"--output_base=" + t.outputBase}

	// Phase 1: loading + analysis only (analogous to nix-instantiate).
	analysisArgs := append(append([]string{}, baseArgs...), "build", "--nobuild", t.target)
	analysisElapsed, _, analysisStderr, err := runBazelCommand(t.workspace, analysisArgs)
	if err != nil {
		tail := analysisStderr
		if len(tail) > t.stderrTail {
			tail = tail[len(tail)-t.stderrTail:]
		}
		return buildResult{}, fmt.Errorf("bazel build --nobuild failed: %w\n%s", err, tail)
	}

	// Phase 2: full build (analogous to nix-store --realise).
	buildArgs := append(append([]string{}, baseArgs...), "build", t.target)
	buildElapsed, _, buildStderr, err := runBazelCommand(t.workspace, buildArgs)
	if err != nil {
		tail := buildStderr
		if len(tail) > t.stderrTail {
			tail = tail[len(tail)-t.stderrTail:]
		}
		return buildResult{}, fmt.Errorf("bazel build failed: %w\n%s", err, tail)
	}

	// Count actions executed from Bazel's summary output.
	built := parseBazelActionCount(buildStderr)

	return buildResult{
		total:     analysisElapsed + buildElapsed,
		evalTime:  analysisElapsed,
		buildTime: buildElapsed,
		built:     built,
	}, nil
}

func (t *bazelTool) Cleanup() error {
	// Expunge removes the output base entirely, including read-only
	// sandbox artifacts that os.RemoveAll cannot delete.
	args := []string{"--output_base=" + t.outputBase, "clean", "--expunge"}
	_, _, _, _ = runBazelCommand(t.workspace, args)

	args = []string{"--output_base=" + t.outputBase, "shutdown"}
	_, _, _, _ = runBazelCommand(t.workspace, args)

	return nil
}

// runBazelCommand runs bazel with the given args in the specified workspace
// directory and returns wall time, stdout, stderr, and any error.
func runBazelCommand(workspace string, args []string) (time.Duration, string, string, error) {
	cmd := exec.Command("bazel", args...)
	cmd.Dir = workspace

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	return elapsed, stdout.String(), stderr.String(), err
}

// parseBazelActionCount extracts the total action count from Bazel's stderr
// summary line like "INFO: 42 processes: 10 internal, 32 linux-sandbox."
func parseBazelActionCount(stderr string) int {
	m := bazelProcessRe.FindStringSubmatch(stderr)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func resolvePaths(repoRoot string) (nixpkgsPath, pluginPath, gomodcache string, err error) {
	// Honor $NIXPKGS_PATH so callers can pin nixpkgs without going
	// through the flake registry (which makes a network call to GitHub
	// and is rate-limited). Falls back to `nix eval nixpkgs#path` so
	// the default flake-registry behavior still works.
	var out string
	nixpkgsPath = strings.TrimSpace(os.Getenv("NIXPKGS_PATH"))
	if nixpkgsPath == "" {
		_, out, _, _ = runCommand("nix", []string{"eval", "--raw", "nixpkgs#path"}, nil)
		nixpkgsPath = strings.TrimSpace(out)
	}
	if nixpkgsPath == "" {
		return "", "", "", fmt.Errorf("could not resolve nixpkgs path (set NIXPKGS_PATH or fix flake registry)")
	}
	_, out, _, _ = runCommand("nix",
		[]string{"build", repoRoot + "#go2nix-nix-plugin", "--no-link", "--print-out-paths"}, nil)
	pluginOut := strings.TrimSpace(out)
	if pluginOut == "" {
		return "", "", "", fmt.Errorf("nix build go2nix-nix-plugin produced no output")
	}
	pluginPath = pluginOut + "/lib/nix/plugins/libgo2nix_plugin.so"

	gomodcache = os.Getenv("GOMODCACHE")
	if gomodcache == "" {
		_, out, _, _ = runCommand("go", []string{"env", "GOMODCACHE"}, nil)
		gomodcache = strings.TrimSpace(out)
	}
	return nixpkgsPath, pluginPath, gomodcache, nil
}

func writeNixExpr(tmpdir, name, tmpl, fixturePath, go2nixSrc, system, mr, subPkg, extraAttrs string) (string, error) {
	content := fmt.Sprintf(tmpl, fixturePath, system, go2nixSrc, go2nixSrc, mr, mr, subPkg, extraAttrs)
	path := filepath.Join(tmpdir, "bench-"+name+".nix")
	return path, os.WriteFile(path, []byte(content), 0o644)
}

func runTouchBenchmark(tools []buildTool, fixtureCopy, scenario, scenarioPath, touchMode string, runs int) []result {
	fmt.Printf("\n%s\nSCENARIO: %s (%s) -- touch %s\n%s\n",
		strings.Repeat("=", 60), scenario, touchMode, scenarioPath, strings.Repeat("=", 60))

	results := make(map[string]*result, len(tools))
	for _, t := range tools {
		results[t.Name()] = &result{Scenario: scenario + "-" + touchMode, Tool: t.Name()}
	}

	fmt.Println("  Warming caches...")
	for _, t := range tools {
		if _, err := t.Build(fixtureCopy); err != nil {
			fmt.Fprintf(os.Stderr, "  FATAL: warmup failed for %s: %v\n", t.Name(), err)
			os.Exit(1)
		}
	}

	filePath := filepath.Join(fixtureCopy, scenarioPath)
	pristine, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("  ERROR: cannot read %s: %v\n", filePath, err)
		return nil
	}

	for runIdx := 1; runIdx <= runs; runIdx++ {
		fmt.Printf("\n  Run %d/%d:\n", runIdx, runs)
		if err := touchFile(filePath, touchMode); err != nil {
			fmt.Printf("    touch failed: %v\n", err)
			continue
		}
		for _, t := range tools {
			br, err := t.Build(fixtureCopy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  FATAL: build failed for %s: %v\n", t.Name(), err)
				os.Exit(1)
			}
			s := br.total.Seconds()
			results[t.Name()].Times = append(results[t.Name()].Times, s)
			results[t.Name()].EvalTimes = append(results[t.Name()].EvalTimes, br.evalTime.Seconds())
			results[t.Name()].BuildTimes = append(results[t.Name()].BuildTimes, br.buildTime.Seconds())
			results[t.Name()].Builds = append(results[t.Name()].Builds, br.built)
			fmt.Printf("    [%s] %.2fs (eval %.2fs + build %.2fs) -- %d drvs built\n",
				t.Name(), s, br.evalTime.Seconds(), br.buildTime.Seconds(), br.built)
		}
		if err := os.WriteFile(filePath, pristine, 0o644); err != nil {
			fmt.Printf("    restore failed: %v\n", err)
		}
	}

	_ = os.WriteFile(filePath, pristine, 0o644)
	out := make([]result, 0, len(tools))
	for _, t := range tools {
		out = append(out, *results[t.Name()])
	}
	return out
}

func runNoChangeBenchmark(tools []buildTool, fixtureCopy string, runs int) []result {
	fmt.Printf("\n%s\nSCENARIO: no-change (cache validation overhead)\n%s\n",
		strings.Repeat("=", 60), strings.Repeat("=", 60))

	results := make(map[string]*result, len(tools))
	for _, t := range tools {
		results[t.Name()] = &result{Scenario: "no_change", Tool: t.Name()}
	}

	fmt.Println("  Warming caches...")
	for _, t := range tools {
		if _, err := t.Build(fixtureCopy); err != nil {
			fmt.Fprintf(os.Stderr, "  FATAL: warmup failed for %s: %v\n", t.Name(), err)
			os.Exit(1)
		}
	}

	for runIdx := 1; runIdx <= runs; runIdx++ {
		fmt.Printf("\n  Run %d/%d:\n", runIdx, runs)
		for _, t := range tools {
			br, err := t.Build(fixtureCopy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  FATAL: build failed for %s: %v\n", t.Name(), err)
				os.Exit(1)
			}
			s := br.total.Seconds()
			results[t.Name()].Times = append(results[t.Name()].Times, s)
			results[t.Name()].EvalTimes = append(results[t.Name()].EvalTimes, br.evalTime.Seconds())
			results[t.Name()].BuildTimes = append(results[t.Name()].BuildTimes, br.buildTime.Seconds())
			results[t.Name()].Builds = append(results[t.Name()].Builds, br.built)
			fmt.Printf("    [%s] %.2fs (eval %.2fs + build %.2fs) -- %d drvs built\n",
				t.Name(), s, br.evalTime.Seconds(), br.buildTime.Seconds(), br.built)
		}
	}

	out := make([]result, 0, len(tools))
	for _, t := range tools {
		out = append(out, *results[t.Name()])
	}
	return out
}

// significant: two means are "significantly different" if their 1σ
// bands don't overlap. Loose by hyperfine standards but enough to flag
// noise.
func significant(winner, runnerUp result) bool {
	if len(winner.Times) == 0 || len(runnerUp.Times) == 0 {
		return false
	}
	wm, ws := mean(winner.Times), stddev(winner.Times)
	rm, rs := mean(runnerUp.Times), stddev(runnerUp.Times)
	return (wm + ws) < (rm - rs)
}

func formatResults(allResults [][]result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\nBENCHMARK RESULTS SUMMARY\n%s\n",
		strings.Repeat("=", 70), strings.Repeat("=", 70))
	if len(allResults) == 0 || len(allResults[0]) == 0 {
		b.WriteString("(no results)\n")
		return b.String()
	}

	tools := make([]string, 0, len(allResults[0]))
	for _, r := range allResults[0] {
		tools = append(tools, r.Tool)
	}
	headers := []string{"Scenario"}
	for _, t := range tools {
		headers = append(headers, t+" (s / drvs)")
	}
	headers = append(headers, "Winner", "Speedup")
	header := "| " + strings.Join(headers, " | ") + " |"
	parts := strings.Split(header, "|")
	var sepCells []string
	for _, c := range parts[1 : len(parts)-1] {
		sepCells = append(sepCells, strings.Repeat("-", len(c)+2))
	}
	sep := "|" + strings.Join(sepCells, "|") + "|"
	fmt.Fprintf(&b, "\n%s\n%s\n", header, sep)

	for _, scenarioResults := range allResults {
		byTool := make(map[string]result, len(scenarioResults))
		for _, r := range scenarioResults {
			byTool[r.Tool] = r
		}
		scenarioName := scenarioResults[0].Scenario
		var winnerName string
		winnerMean := math.Inf(1)
		for name, r := range byTool {
			m := mean(r.Times)
			if m < winnerMean {
				winnerMean = m
				winnerName = name
			}
		}
		winner := byTool[winnerName]
		others := make([]result, 0, len(byTool)-1)
		for _, r := range scenarioResults {
			if r.Tool != winnerName {
				others = append(others, r)
			}
		}
		sort.Slice(others, func(i, j int) bool { return mean(others[i].Times) < mean(others[j].Times) })

		var winnerLabel, speedup string
		switch {
		case len(others) > 0 && significant(winner, others[0]):
			winnerLabel = "**" + winnerName + "**"
			wm := mean(winner.Times)
			if wm > 0 {
				speedup = fmt.Sprintf("%.2fx", mean(others[0].Times)/wm)
			} else {
				speedup = "--"
			}
		case len(others) > 0:
			winnerLabel = "tie"
			speedup = "n.s."
		default:
			winnerLabel = "**" + winnerName + "**"
			speedup = "--"
		}
		var cells []string
		for _, t := range tools {
			r := byTool[t]
			cells = append(cells, fmt.Sprintf("%.2f / %.0f", mean(r.Times), meanInt(r.Builds)))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", scenarioName, strings.Join(cells, " | "), winnerLabel, speedup)
	}

	b.WriteString("\n## Detailed Results\n\n")
	for _, scenarioResults := range allResults {
		fmt.Fprintf(&b, "### %s\n\n", scenarioResults[0].Scenario)
		for _, r := range scenarioResults {
			fmt.Fprintf(&b, "**%s:**\n", r.Tool)
			fmt.Fprintf(&b, "  - Wall:  %.2fs (+/-%.2fs) range %.2fs..%.2fs\n",
				mean(r.Times), stddev(r.Times), minF(r.Times), maxF(r.Times))
			fmt.Fprintf(&b, "  - Eval:  %.2fs (+/-%.2fs) range %.2fs..%.2fs\n",
				mean(r.EvalTimes), stddev(r.EvalTimes), minF(r.EvalTimes), maxF(r.EvalTimes))
			fmt.Fprintf(&b, "  - Build: %.2fs (+/-%.2fs) range %.2fs..%.2fs\n",
				mean(r.BuildTimes), stddev(r.BuildTimes), minF(r.BuildTimes), maxF(r.BuildTimes))
			fmt.Fprintf(&b, "  - Drvs built: %.1f (per-run: %v)\n\n", meanInt(r.Builds), r.Builds)
		}
	}
	return b.String()
}

func exportJSON(allResults [][]result, outputPath string) error {
	type toolEntry struct {
		Times      []float64 `json:"times"`
		Mean       float64   `json:"mean"`
		Stddev     float64   `json:"stddev"`
		Builds     []int     `json:"builds"`
		BuildsMean float64   `json:"builds_mean"`
	}
	type scenarioEntry struct {
		Name  string               `json:"name"`
		Tools map[string]toolEntry `json:"tools"`
	}
	data := struct {
		Timestamp string          `json:"timestamp"`
		Scenarios []scenarioEntry `json:"scenarios"`
	}{
		Timestamp: time.Now().Format(time.RFC3339),
	}
	for _, scenarioResults := range allResults {
		entry := scenarioEntry{
			Name:  scenarioResults[0].Scenario,
			Tools: make(map[string]toolEntry, len(scenarioResults)),
		}
		for _, r := range scenarioResults {
			entry.Tools[r.Tool] = toolEntry{
				Times:      r.Times,
				Mean:       mean(r.Times),
				Stddev:     stddev(r.Times),
				Builds:     r.Builds,
				BuildsMean: meanInt(r.Builds),
			}
		}
		data.Scenarios = append(data.Scenarios, entry)
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	fmt.Printf("\nResults exported to: %s\n", outputPath)
	return nil
}

func getRepoRoot() (string, error) {
	_, out, _, _ := runCommand("git", []string{"rev-parse", "--show-toplevel"}, nil)
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("git rev-parse failed")
	}
	return root, nil
}

func detectSystem() (string, error) {
	_, out, _, _ := runCommand("nix",
		[]string{"eval", "--raw", "--impure", "--expr", "builtins.currentSystem"}, nil)
	system := strings.TrimSpace(out)
	if system == "" {
		return "", fmt.Errorf("nix eval currentSystem failed")
	}
	return system, nil
}

// scenarioPath looks up the touch path for a scenario name.
func scenarioPath(fc fixtureConfig, name string) (string, bool) {
	for _, s := range fc.scenarios {
		if s.name == name {
			return s.path, true
		}
	}
	return "", false
}

func main() {
	runs := flag.Int("runs", 3, "number of runs per scenario")
	scenario := flag.String("scenario", "all",
		"scenario to run (no_change|leaf|mid|deep|all)")
	touchMode := flag.String("touch-mode", "private",
		"edit type: private=internal symbol, exported=API change")
	toolsCSV := flag.String("tools", "nix-nocgo,nix-ca-nocgo",
		"comma-separated tools (nix,nix-ca,nix-nocgo,nix-ca-nocgo,nix-dynamic,nix-dynamic-nocgo,bazel)")
	fixtureName := flag.String("fixture", "light",
		"fixture to use (torture|light)")
	jsonOut := flag.String("json", "", "export results as JSON to this path")
	assertCascade := flag.Int("assert-cascade", -1,
		"fail if any tool builds more than N drvs on a touch scenario")
	stderrTail := flag.Int("stderr-tail", 500,
		"bytes of stderr to keep in error messages")
	flag.Parse()

	fc, ok := fixtures[*fixtureName]
	if !ok {
		names := make([]string, 0, len(fixtures))
		for k := range fixtures {
			names = append(names, k)
		}
		fmt.Fprintf(os.Stderr, "unknown fixture: %q (available: %v)\n", *fixtureName, names)
		os.Exit(2)
	}

	repoRoot, err := getRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fixtureSrc := filepath.Join(repoRoot, "tests", "fixtures", fc.dir)

	tmpBase := os.Getenv("TMPDIR")
	if tmpBase == "" {
		tmpBase = "/tmp"
	}

	tmpdir := filepath.Join(tmpBase, "bench-incremental")
	if err := os.MkdirAll(tmpdir, 0o755); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Compute fixture copy path early so tools can reference it.
	fixtureCopy := filepath.Join(tmpBase, "bench-fixture-copy")

	// Parse requested tools first so we can skip expensive Nix setup
	// when only non-Nix tools (e.g. bazel) are requested.
	requested := strings.Split(*toolsCSV, ",")

	needsNix := false
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if strings.HasPrefix(name, "nix") {
			needsNix = true
			break
		}
	}

	available := make(map[string]buildTool)

	// Nix tools: only set up when at least one nix-* tool is requested.
	if needsNix {
		fmt.Println("Resolving Nix dependencies...")

		nixpkgsPath, pluginPath, gomodcache, err := resolvePaths(repoRoot)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		system, err := detectSystem()
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		// Shared local store for nix tools — same store, same sandbox=false,
		// no daemon. CA features are enabled client-side so the system daemon
		// config doesn't matter. Persistent across invocations so the cold
		// cache cost is paid once.
		storeRoot := filepath.Join(tmpdir, "store")
		if err := os.MkdirAll(storeRoot, 0o755); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		mr := fc.modRoot
		sp := fc.subPackage
		exprNix, err := writeNixExpr(tmpdir, "nix", exprTemplate, fixtureSrc, repoRoot, system, mr, sp, "")
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		exprCA, err := writeNixExpr(tmpdir, "nix-ca", exprTemplate, fixtureSrc, repoRoot, system, mr, sp, "contentAddressed = true;")
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		exprNoCgo, err := writeNixExpr(tmpdir, "nix-nocgo", exprTemplate, fixtureSrc, repoRoot, system, mr, sp, "CGO_ENABLED = \"0\";")
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		exprCANoCgo, err := writeNixExpr(tmpdir, "nix-ca-nocgo", exprTemplate, fixtureSrc, repoRoot, system, mr, sp, "contentAddressed = true; CGO_ENABLED = \"0\";")
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		exprDyn, err := writeNixExpr(tmpdir, "nix-dynamic", dynExprTemplate, fixtureSrc, repoRoot, system, mr, sp, "")
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		exprDynNoCgo, err := writeNixExpr(tmpdir, "nix-dynamic-nocgo", dynExprTemplate, fixtureSrc, repoRoot, system, mr, sp, `CGO_ENABLED = "0";`)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		caOpts := []string{"--option", "extra-experimental-features", "ca-derivations"}
		dynOpts := []string{
			"--option", "extra-experimental-features", "dynamic-derivations ca-derivations recursive-nix",
			"--option", "extra-system-features", "recursive-nix",
		}

		available["nix"] = &nixTool{
			name: "nix", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprNix, storeRoot: storeRoot,
			stderrTail: *stderrTail,
		}
		available["nix-ca"] = &nixTool{
			name: "nix-ca", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprCA, storeRoot: storeRoot,
			extraOpts: caOpts, stderrTail: *stderrTail,
		}
		available["nix-nocgo"] = &nixTool{
			name: "nix-nocgo", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprNoCgo, storeRoot: storeRoot,
			stderrTail: *stderrTail,
		}
		available["nix-ca-nocgo"] = &nixTool{
			name: "nix-ca-nocgo", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprCANoCgo, storeRoot: storeRoot,
			extraOpts: caOpts, stderrTail: *stderrTail,
		}
		// Dynamic tools use the host store (storeRoot=""). The recursive-nix
		// inner daemon serves whichever store the outer build runs against,
		// and a rooted store doesn't have the FOD inputDrvs the inner
		// `go2nix resolve` registers — AddToStore fails with "path is not
		// valid". The host store does. Less isolated than the dag tools'
		// rooted store, but the wrapper drv is unique per fixture path so
		// runs don't interfere.
		available["nix-dynamic"] = &nixTool{
			name: "nix-dynamic", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprDyn,
			extraOpts: dynOpts, stderrTail: *stderrTail, skipOnFail: true,
		}
		available["nix-dynamic-nocgo"] = &nixTool{
			name: "nix-dynamic-nocgo", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprDynNoCgo,
			extraOpts: dynOpts, stderrTail: *stderrTail, skipOnFail: true,
		}
	}

	// Bazel tool: only available for fixtures that have BUILD files.
	if fc.hasBazel {
		bazelTarget := "//" + fc.modRoot + "/" + strings.TrimPrefix(fc.subPackage, "./")
		available["bazel"] = &bazelTool{
			name:       "bazel",
			workspace:  fixtureCopy,
			target:     bazelTarget,
			outputBase: filepath.Join(tmpdir, "bazel-output-base"),
			stderrTail: *stderrTail,
		}
	}

	tools := make([]buildTool, 0, len(requested))
	for _, name := range requested {
		name = strings.TrimSpace(name)
		t, ok := available[name]
		if !ok {
			if name == "bazel" && !fc.hasBazel {
				fmt.Fprintf(os.Stderr, "tool %q is not available for fixture %q (no Bazel BUILD files; try -fixture torture)\n", name, *fixtureName)
			} else {
				names := make([]string, 0, len(available))
				for k := range available {
					names = append(names, k)
				}
				sort.Strings(names)
				fmt.Fprintf(os.Stderr, "unknown tool: %q (available: %v)\n", name, names)
			}
			os.Exit(2)
		}
		tools = append(tools, t)
	}

	// Copy fixture once; touched files are restored in-place per run.
	if err = os.RemoveAll(fixtureCopy); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err = copyTree(fixtureSrc, fixtureCopy); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Probe each tool once. nix-dynamic* needs recursive-nix in the build
	// sandbox; on stores or remotes that don't provide it the wrapper build
	// fails — drop those tools with a notice rather than aborting the whole
	// run, mirroring benchmark-build's "SKIPPED (ca-derivations not enabled)"
	// behaviour. Other tools still fail fast.
	fmt.Println("Probing tools...")
	probed := tools[:0]
	for _, t := range tools {
		if _, err := t.Build(fixtureCopy); err != nil {
			if t.SkipOnFail() {
				fmt.Printf("  SKIP %s: %v\n", t.Name(), err)
				continue
			}
			fmt.Fprintf(os.Stderr, "  FATAL: probe failed for %s: %v\n", t.Name(), err)
			os.Exit(1)
		}
		fmt.Printf("  OK   %s\n", t.Name())
		probed = append(probed, t)
	}
	tools = probed
	if len(tools) == 0 {
		fmt.Fprintln(os.Stderr, "no runnable tools after probe")
		os.Exit(1)
	}

	fmt.Printf("\n%s\nINCREMENTAL BUILD BENCHMARK\n%s\n",
		strings.Repeat("=", 70), strings.Repeat("=", 70))
	fmt.Printf("Fixture:    %s/%s\n", fc.dir, fc.modRoot)
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	fmt.Printf("Tools:      %s\n", strings.Join(names, ", "))
	fmt.Printf("Touch mode: %s\n", *touchMode)
	fmt.Printf("Runs:       %d\n", *runs)

	var scenarios []string
	if *scenario == "all" {
		scenarios = append(scenarios, "no_change")
		for _, s := range fc.scenarios {
			scenarios = append(scenarios, s.name)
		}
	} else {
		scenarios = []string{*scenario}
	}

	var allResults [][]result
	for _, name := range scenarios {
		if name == "no_change" {
			allResults = append(allResults, runNoChangeBenchmark(tools, fixtureCopy, *runs))
			continue
		}
		path, ok := scenarioPath(fc, name)
		if !ok {
			_, _ = fmt.Fprintf(os.Stderr, "unknown scenario: %s\n", name)
			os.Exit(2)
		}
		allResults = append(allResults, runTouchBenchmark(tools, fixtureCopy, name, path, *touchMode, *runs))
	}

	fmt.Print(formatResults(allResults))

	if *jsonOut != "" {
		if err = exportJSON(allResults, *jsonOut); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// Cleanup tools (e.g. bazel shutdown) then temp dirs.
	for _, t := range tools {
		_ = t.Cleanup()
	}
	_ = os.RemoveAll(fixtureCopy)
	_ = os.RemoveAll(tmpdir)
	// storeRoot intentionally NOT removed: leaving the populated rooted
	// store on disk lets the next run start warm.

	// Cascade-size regression check.
	if *assertCascade >= 0 {
		var violations []string
		for _, scenarioResults := range allResults {
			for _, r := range scenarioResults {
				if r.Scenario == "no_change" {
					continue
				}
				if maxInt(r.Builds) > *assertCascade {
					violations = append(violations, fmt.Sprintf(
						"%s/%s: built %d drvs (threshold %d)",
						r.Scenario, r.Tool, maxInt(r.Builds), *assertCascade))
				}
			}
		}
		if len(violations) > 0 {
			fmt.Printf("\n%s\nFAIL: cascade-size threshold (%d) exceeded\n%s\n",
				strings.Repeat("=", 70), *assertCascade, strings.Repeat("=", 70))
			for _, v := range violations {
				fmt.Printf("  %s\n", v)
			}
			os.Exit(1)
		}
		fmt.Printf("\nPASS: all tools stayed within cascade threshold %d\n", *assertCascade)
	}
}

// copyTree mirrors src to dst — files copied, directories created with
// the same mode, symlinks resolved (the fixture has none). Used once
// per benchmark run, so cp's recursive walk would also work but staying
// in-process avoids a coreutils dep.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}

		defer func() { _ = in.Close() }() //nolint:errcheck

		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}

		if _, err = io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}

		return out.Close()
	})
}
