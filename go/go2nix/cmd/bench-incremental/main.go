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
	"sort"
	"strings"
	"time"
)

// touchScenarios maps a scenario name to the path (relative to the
// fixture root) of the file the benchmark touches before rebuilding.
var touchScenarios = []struct {
	name string
	path string
}{
	// Leaf: only main depends on this. Cascade = main + link.
	{"leaf", "app-full/cmd/app-full/main.go"},
	// Mid: aws is imported by main only. Cascade = aws + main + link.
	{"mid", "internal/aws/aws.go"},
	// Deep: common is imported by ~9 local modules + main.
	{"deep", "internal/common/common.go"},
}

const (
	modRoot      = "app-full"
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
  pname = "torture-bench";
  version = "0.0.1";
  subPackages = [ "./cmd/app-full" ];
  doCheck = false;
  %s
}
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
	Scenario string
	Tool     string
	Times    []float64
	Builds   []int
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

// runCommand runs cmd with the given env overlay and returns wall time
// + combined stdout/stderr. Failure prints the tail of stderr but does
// NOT exit — the caller decides what a failure means for the benchmark.
func runCommand(name string, args []string, env map[string]string) (time.Duration, string, string) {
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
	if err != nil {
		tail := stderr.String()
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		fmt.Printf("  COMMAND FAILED (%v):\n  stderr: %s\n", err, tail)
	}
	return elapsed, stdout.String(), stderr.String()
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

type nixTool struct {
	name        string
	nixpkgsPath string
	pluginPath  string
	gomodcache  string
	exprPath    string
	extraOpts   []string
}

func (t *nixTool) build(srcPath string) (time.Duration, int) {
	args := []string{
		"-I", "nixpkgs=" + t.nixpkgsPath,
		"--option", "plugin-files", t.pluginPath,
		"--option", "allow-import-from-derivation", "true",
	}
	args = append(args, t.extraOpts...)
	args = append(args, t.exprPath, "--no-out-link")
	if srcPath != "" {
		args = append(args, "--arg", "srcPath", srcPath)
	}
	env := map[string]string{
		"GOMODCACHE": t.gomodcache,
	}
	elapsed, stdout, stderr := runCommand("nix-build", args, env)
	// Each per-derivation `building '/nix/store/...'` line is one drv.
	// In CA mode each drv prints one "building" then one "resolved
	// derivation" — we count "building" only.
	built := strings.Count(stdout+stderr, "building '/nix/store/")
	return elapsed, built
}

func resolvePaths(repoRoot string) (nixpkgsPath, pluginPath, gomodcache string, err error) {
	// Honor $NIXPKGS_PATH so callers can pin nixpkgs without going
	// through the flake registry (which makes a network call to GitHub
	// and is rate-limited). Falls back to `nix eval nixpkgs#path` so
	// the default flake-registry behavior still works.
	var out string
	nixpkgsPath = strings.TrimSpace(os.Getenv("NIXPKGS_PATH"))
	if nixpkgsPath == "" {
		_, out, _ = runCommand("nix", []string{"eval", "--raw", "nixpkgs#path"}, nil)
		nixpkgsPath = strings.TrimSpace(out)
	}
	if nixpkgsPath == "" {
		return "", "", "", fmt.Errorf("could not resolve nixpkgs path (set NIXPKGS_PATH or fix flake registry)")
	}
	_, out, _ = runCommand("nix",
		[]string{"build", repoRoot + "#go2nix-nix-plugin", "--no-link", "--print-out-paths"}, nil)
	pluginOut := strings.TrimSpace(out)
	if pluginOut == "" {
		return "", "", "", fmt.Errorf("nix build go2nix-nix-plugin produced no output")
	}
	pluginPath = pluginOut + "/lib/nix/plugins/libgo2nix_plugin.so"

	gomodcache = os.Getenv("GOMODCACHE")
	if gomodcache == "" {
		_, out, _ = runCommand("go", []string{"env", "GOMODCACHE"}, nil)
		gomodcache = strings.TrimSpace(out)
	}
	return nixpkgsPath, pluginPath, gomodcache, nil
}

func writeNixExpr(tmpdir, name, fixturePath, go2nixSrc, system, extraAttrs string) (string, error) {
	content := fmt.Sprintf(exprTemplate, fixturePath, system, go2nixSrc, go2nixSrc, modRoot, modRoot, extraAttrs)
	path := filepath.Join(tmpdir, "bench-"+name+".nix")
	return path, os.WriteFile(path, []byte(content), 0o644)
}

func runTouchBenchmark(tools []*nixTool, fixtureCopy, scenario, scenarioPath, touchMode string, runs int) []result {
	fmt.Printf("\n%s\nSCENARIO: %s (%s) -- touch %s\n%s\n",
		strings.Repeat("=", 60), scenario, touchMode, scenarioPath, strings.Repeat("=", 60))

	results := make(map[string]*result, len(tools))
	for _, t := range tools {
		results[t.name] = &result{Scenario: scenario + "-" + touchMode, Tool: t.name}
	}

	fmt.Println("  Warming caches...")
	for _, t := range tools {
		t.build(fixtureCopy)
	}

	filePath := filepath.Join(fixtureCopy, scenarioPath)
	pristine, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("  ERROR: cannot read %s: %v\n", filePath, err)
		return nil
	}

	for runIdx := 1; runIdx <= runs; runIdx++ {
		fmt.Printf("\n  Run %d/%d:\n", runIdx, runs)
		// Touch once per run so all tools see byte-identical input.
		// A per-tool touch produces a different rotating value (and
		// timestamp) per tool, which is fine for `private` mode but
		// a fairness issue for `exported` — cascade size depends on
		// the touched file's exact bytes.
		if err := touchFile(filePath, touchMode); err != nil {
			fmt.Printf("    touch failed: %v\n", err)
			continue
		}
		for _, t := range tools {
			elapsed, built := t.build(fixtureCopy)
			s := elapsed.Seconds()
			results[t.name].Times = append(results[t.name].Times, s)
			results[t.name].Builds = append(results[t.name].Builds, built)
			fmt.Printf("    [%s] %.2fs -- %d drvs built\n", t.name, s, built)
		}
		// Restore after all tools have built; faster than copying the
		// whole fixture each iteration.
		if err := os.WriteFile(filePath, pristine, 0o644); err != nil {
			fmt.Printf("    restore failed: %v\n", err)
		}
	}

	// Always restore.
	_ = os.WriteFile(filePath, pristine, 0o644)
	out := make([]result, 0, len(tools))
	for _, t := range tools {
		out = append(out, *results[t.name])
	}
	return out
}

func runNoChangeBenchmark(tools []*nixTool, fixtureCopy string, runs int) []result {
	fmt.Printf("\n%s\nSCENARIO: no-change (cache validation overhead)\n%s\n",
		strings.Repeat("=", 60), strings.Repeat("=", 60))

	results := make(map[string]*result, len(tools))
	for _, t := range tools {
		results[t.name] = &result{Scenario: "no_change", Tool: t.name}
	}

	fmt.Println("  Warming caches...")
	for _, t := range tools {
		t.build(fixtureCopy)
	}

	for runIdx := 1; runIdx <= runs; runIdx++ {
		fmt.Printf("\n  Run %d/%d:\n", runIdx, runs)
		for _, t := range tools {
			elapsed, built := t.build(fixtureCopy)
			s := elapsed.Seconds()
			results[t.name].Times = append(results[t.name].Times, s)
			results[t.name].Builds = append(results[t.name].Builds, built)
			fmt.Printf("    [%s] %.2fs -- %d drvs built\n", t.name, s, built)
		}
	}

	out := make([]result, 0, len(tools))
	for _, t := range tools {
		out = append(out, *results[t.name])
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
		var winnerMean float64 = math.Inf(1)
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
			fmt.Fprintf(&b, "  - Wall: %.2fs (+/-%.2fs) range %.2fs..%.2fs\n",
				mean(r.Times), stddev(r.Times), minF(r.Times), maxF(r.Times))
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
	_, out, _ := runCommand("git", []string{"rev-parse", "--show-toplevel"}, nil)
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("git rev-parse failed")
	}
	return root, nil
}

func detectSystem() (string, error) {
	_, out, _ := runCommand("nix",
		[]string{"eval", "--raw", "--impure", "--expr", "builtins.currentSystem"}, nil)
	system := strings.TrimSpace(out)
	if system == "" {
		return "", fmt.Errorf("nix eval currentSystem failed")
	}
	return system, nil
}

// scenarioPath looks up the touch path for a scenario name.
func scenarioPath(name string) (string, bool) {
	for _, s := range touchScenarios {
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
	toolsCSV := flag.String("tools", "nix,nix-ca",
		"comma-separated tools (default: nix,nix-ca)")
	jsonOut := flag.String("json", "", "export results as JSON to this path")
	assertCascade := flag.Int("assert-cascade", -1,
		"fail if any tool builds more than N drvs on a touch scenario")
	flag.Parse()

	repoRoot, err := getRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fixtureSrc := filepath.Join(repoRoot, "tests", "fixtures", "torture-project")

	fmt.Println("Resolving dependencies...")
	nixpkgsPath, pluginPath, gomodcache, err := resolvePaths(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	system, err := detectSystem()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	tmpBase := os.Getenv("TMPDIR")
	if tmpBase == "" {
		tmpBase = "/tmp"
	}
	tmpdir := filepath.Join(tmpBase, "bench-incremental")
	if err := os.MkdirAll(tmpdir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Shared rooted store for both tools — same store guarantees a fair
	// comparison (no daemon, no socat, no sandbox=false). Persistent
	// across invocations: cold cache only matters once. Use --fresh to
	// force a wipe.
	exprNix, err := writeNixExpr(tmpdir, "nix", fixtureSrc, repoRoot, system, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exprCA, err := writeNixExpr(tmpdir, "nix-ca", fixtureSrc, repoRoot, system, "contentAddressed = true;")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	available := map[string]*nixTool{
		"nix": {
			name: "nix", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprNix,
		},
		"nix-ca": {
			name: "nix-ca", nixpkgsPath: nixpkgsPath, pluginPath: pluginPath,
			gomodcache: gomodcache, exprPath: exprCA,
			extraOpts: []string{
				"--option", "extra-experimental-features", "ca-derivations",
			},
		},
	}

	requested := strings.Split(*toolsCSV, ",")
	tools := make([]*nixTool, 0, len(requested))
	for _, name := range requested {
		name = strings.TrimSpace(name)
		t, ok := available[name]
		if !ok {
			names := make([]string, 0, len(available))
			for k := range available {
				names = append(names, k)
			}
			fmt.Fprintf(os.Stderr, "unknown tool: %q (available: %v)\n", name, names)
			os.Exit(2)
		}
		tools = append(tools, t)
	}

	// Copy fixture once; touched files are restored in-place per run.
	fixtureCopy := filepath.Join(tmpBase, "bench-fixture-copy")
	if err := os.RemoveAll(fixtureCopy); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := copyTree(fixtureSrc, fixtureCopy); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("\n%s\nGO2NIX INCREMENTAL BUILD BENCHMARK\n%s\n",
		strings.Repeat("=", 70), strings.Repeat("=", 70))
	fmt.Println("Fixture:    torture-project/app-full")
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.name
	}
	fmt.Printf("Tools:      %s\n", strings.Join(names, ", "))
	fmt.Printf("Touch mode: %s\n", *touchMode)
	fmt.Printf("Runs:       %d\n", *runs)
	fmt.Printf("Store:      user's main /nix/store\n")

	var scenarios []string
	if *scenario == "all" {
		scenarios = append(scenarios, "no_change")
		for _, s := range touchScenarios {
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
		path, ok := scenarioPath(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown scenario: %s\n", name)
			os.Exit(2)
		}
		allResults = append(allResults, runTouchBenchmark(tools, fixtureCopy, name, path, *touchMode, *runs))
	}

	fmt.Print(formatResults(allResults))

	if *jsonOut != "" {
		if err := exportJSON(allResults, *jsonOut); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// Cleanup before asserting so a failure doesn't leak the store.
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
		defer in.Close() //nolint:errcheck
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
