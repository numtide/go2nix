#!/usr/bin/env python3
"""
Benchmark: incremental build comparison for go2nix modes.

Measures rebuild time after touching a single file at different dep-graph
depths and with different edit types (private vs exported symbols).

Usage:
    python tests/bench-incremental.py [--runs N] [--scenario S] [--tools nix,nix-ca]

Requires: nix with go2nix-nix-plugin, socat (for CA mode).
"""

import argparse
import json
import os
import shutil
import statistics
import subprocess
import time
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path


@dataclass
class BenchmarkResult:
    scenario: str
    tool: str
    times: list[float] = field(default_factory=list)
    cache_info: str = ""

    @property
    def mean(self) -> float:
        return statistics.mean(self.times) if self.times else 0.0

    @property
    def stddev(self) -> float:
        return statistics.stdev(self.times) if len(self.times) > 1 else 0.0

    @property
    def min(self) -> float:
        return min(self.times) if self.times else 0.0

    @property
    def max(self) -> float:
        return max(self.times) if self.times else 0.0


# Touch points at different dep-graph depths.
# Paths are relative to the fixture root (torture-project).
TOUCH_SCENARIOS = {
    # Leaf: only main depends on nothing else. Cascade = main + link.
    "leaf": "app-full/cmd/app-full/main.go",
    # Mid: aws is imported by main only. Cascade = aws + main + link.
    "mid": "internal/aws/aws.go",
    # Deep: common is imported by ~9 local modules + main.
    # Cascade = common + all dependents + main + link.
    "deep": "internal/common/common.go",
}

GO_DIR = "app-full"
MOD_ROOT = "app-full"
NIX_EXPR_TEMPLATE = """\
{{ srcPath ? {fixture_path} }}:
let
  pkgs = import <nixpkgs> {{ system = "{system}"; }};
  go2nixLib = import {go2nix_src}/lib.nix {{}};
  goEnv = go2nixLib.mkGoEnv {{
    go = pkgs.go_1_26;
    go2nix = import {go2nix_src}/packages/go2nix {{ inherit pkgs; }};
    inherit (pkgs) callPackage;
  }};
in
goEnv.buildGoApplication {{
  src = srcPath;
  modRoot = "{mod_root}";
  goLock = "${{srcPath}}/{mod_root}/go2nix.toml";
  pname = "torture-bench";
  version = "0.0.1";
  subPackages = [ "./cmd/app-full" ];
  doCheck = false;
  {extra_attrs}
}}
"""

_TOUCH_MARKER = "// BENCHMARK_TOUCH"

_TOUCH_TEMPLATES = {
    "private": "var _benchTouch{ts} = uint64({ts}) {marker}\n",
    "exported": "var BenchTouch{ts} = uint64({ts}) {marker}\n",
}


def get_repo_root() -> Path:
    result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        capture_output=True,
        text=True,
        check=True,
    )
    return Path(result.stdout.strip())


def run_command(
    cmd: list[str],
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
) -> tuple[float, str, str]:
    full_env = os.environ.copy()
    if env:
        full_env.update(env)

    start = time.perf_counter()
    result = subprocess.run(cmd, cwd=cwd, env=full_env, capture_output=True, text=True)
    elapsed = time.perf_counter() - start

    if result.returncode != 0:
        print(f"  COMMAND FAILED (exit {result.returncode}):")
        print(f"  stderr: {result.stderr[-500:]}")

    return elapsed, result.stdout or "", result.stderr or ""


def touch_file(path: Path, mode: str) -> None:
    if not path.exists():
        print(f"  Warning: file not found: {path}")
        return
    content = path.read_text()
    ts = time.time_ns()
    line = _TOUCH_TEMPLATES[mode].format(ts=ts, marker=_TOUCH_MARKER)
    path.write_text(f"{content}\n{line}")


def restore_file(path: Path) -> None:
    if not path.exists():
        return
    lines = path.read_text().splitlines(keepends=True)
    kept = [ln for ln in lines if _TOUCH_MARKER not in ln]
    if len(kept) != len(lines):
        if kept and kept[-1] == "\n":
            kept.pop()
        path.write_text("".join(kept))


class LocalDaemon:
    """Manages a local nix daemon via socat for CA derivation support."""

    def __init__(self, tmpdir: Path, extra_features: str = ""):
        self.store_root = tmpdir / "ca-store"
        self.socket = self.store_root / "daemon.sock"
        self.pid: int | None = None
        self.features = f"nix-command {extra_features}".strip()

    def start(self) -> None:
        # Fresh store each run to avoid stale state.
        if self.store_root.exists():
            shutil.rmtree(self.store_root, ignore_errors=True)
        self.store_root.mkdir(parents=True, exist_ok=True)

        daemon_sh = self.store_root / "daemon.sh"
        daemon_sh.write_text(
            f"#!/usr/bin/env bash\n"
            f'exec nix daemon --stdio \\\n'
            f'  --option experimental-features "{self.features}" \\\n'
            f'  --option sandbox false \\\n'
            f'  --option allow-import-from-derivation true \\\n'
            f'  --store "local?root={self.store_root}"\n'
        )
        daemon_sh.chmod(0o755)

        proc = subprocess.Popen(
            ["socat", f"UNIX-LISTEN:{self.socket},fork", f"EXEC:{daemon_sh}"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        self.pid = proc.pid
        time.sleep(1)

        # Verify the socket exists.
        if not self.socket.exists():
            raise RuntimeError(f"Local daemon failed to start (no socket at {self.socket})")
        print(f"  Local daemon: PID {self.pid}, store {self.store_root}")

    def stop(self) -> None:
        if self.pid:
            try:
                os.kill(self.pid, 15)
            except ProcessLookupError:
                pass
            self.pid = None

    @property
    def env_remote(self) -> str:
        return f"unix://{self.socket}?root={self.store_root}"


class NixTool:
    def __init__(
        self,
        name: str,
        nixpkgs_path: str,
        plugin_path: str,
        gomodcache: str,
        expr_path: str,
        extra_opts: list[str] | None = None,
        daemon: LocalDaemon | None = None,
    ):
        self.name = name
        self.nixpkgs_path = nixpkgs_path
        self.plugin_path = plugin_path
        self.gomodcache = gomodcache
        self.expr_path = expr_path
        self.extra_opts = extra_opts or []
        self.daemon = daemon

    def build(self, src_path: str | None = None) -> tuple[float, str]:
        cmd = [
            "nix-build",
            "-I",
            f"nixpkgs={self.nixpkgs_path}",
            "--option",
            "plugin-files",
            self.plugin_path,
            "--option",
            "allow-import-from-derivation",
            "true",
            *self.extra_opts,
            self.expr_path,
            "--no-out-link",
        ]
        if src_path:
            cmd.extend(["--arg", "srcPath", src_path])

        env: dict[str, str] = {"GOMODCACHE": self.gomodcache}
        if self.daemon:
            env["NIX_REMOTE"] = self.daemon.env_remote
        elapsed, stdout, stderr = run_command(cmd, env=env)
        output = stdout + stderr
        built = output.count("building '/nix/store/")
        cache_info = f"{built} built" if built > 0 else "fully cached"
        return elapsed, cache_info


def resolve_paths(repo_root: Path) -> tuple[str, str, str]:
    """Resolve nixpkgs path, plugin path, and gomodcache."""
    _, nixpkgs_path, _ = run_command(
        ["nix", "eval", "--raw", "nixpkgs#path"]
    )
    _, plugin_out, _ = run_command(
        [
            "nix",
            "build",
            f"{repo_root}#go2nix-nix-plugin",
            "--no-link",
            "--print-out-paths",
        ]
    )
    plugin_path = f"{plugin_out.strip()}/lib/nix/plugins/libgo2nix_plugin.so"

    # Use the pre-built gomodcache from the benchmark package if available,
    # otherwise fall back to user's GOMODCACHE.
    gomodcache = os.environ.get("GOMODCACHE", "")
    if not gomodcache:
        _, gomodcache, _ = run_command(["go", "env", "GOMODCACHE"])
        gomodcache = gomodcache.strip()

    return nixpkgs_path.strip(), plugin_path, gomodcache


def write_nix_expr(
    tmpdir: Path,
    name: str,
    fixture_path: str,
    go2nix_src: str,
    system: str,
    extra_attrs: str = "",
) -> str:
    content = NIX_EXPR_TEMPLATE.format(
        fixture_path=fixture_path,
        go2nix_src=go2nix_src,
        system=system,
        mod_root=MOD_ROOT,
        extra_attrs=extra_attrs,
    )
    path = tmpdir / f"bench-{name}.nix"
    path.write_text(content)
    return str(path)


def run_touch_benchmark(
    tools: list[NixTool],
    fixture_src: Path,
    fixture_copy: Path,
    scenario: str,
    touch_mode: str,
    runs: int,
) -> list[BenchmarkResult]:
    rel_path = TOUCH_SCENARIOS[scenario]
    print(f"\n{'=' * 60}")
    print(f"SCENARIO: {scenario} ({touch_mode}) -- touch {rel_path}")
    print(f"{'=' * 60}")

    results = {t.name: BenchmarkResult(scenario=f"{scenario}-{touch_mode}", tool=t.name) for t in tools}

    # Warm caches
    print("  Warming caches...")
    for tool in tools:
        tool.build(str(fixture_copy))

    file_path = fixture_copy / rel_path

    for run_idx in range(1, runs + 1):
        print(f"\n  Run {run_idx}/{runs}:")
        for tool in tools:
            # Reset fixture to baseline
            shutil.rmtree(fixture_copy)
            shutil.copytree(fixture_src, fixture_copy)

            # Apply touch
            touch_file(file_path, touch_mode)

            elapsed, cache_info = tool.build(str(fixture_copy))
            results[tool.name].times.append(elapsed)
            results[tool.name].cache_info = cache_info
            print(f"    [{tool.name}] {elapsed:.2f}s -- {cache_info}")

    return [results[t.name] for t in tools]


def run_no_change_benchmark(
    tools: list[NixTool],
    fixture_copy: Path,
    runs: int,
) -> list[BenchmarkResult]:
    print(f"\n{'=' * 60}")
    print("SCENARIO: no-change (cache validation overhead)")
    print(f"{'=' * 60}")

    results = {t.name: BenchmarkResult(scenario="no_change", tool=t.name) for t in tools}

    print("  Warming caches...")
    for tool in tools:
        tool.build(str(fixture_copy))

    for run_idx in range(1, runs + 1):
        print(f"\n  Run {run_idx}/{runs}:")
        for tool in tools:
            elapsed, cache_info = tool.build(str(fixture_copy))
            results[tool.name].times.append(elapsed)
            results[tool.name].cache_info = cache_info
            print(f"    [{tool.name}] {elapsed:.2f}s -- {cache_info}")

    return [results[t.name] for t in tools]


def format_results(all_results: list[list[BenchmarkResult]]) -> str:
    lines = [f"\n{'=' * 70}", "BENCHMARK RESULTS SUMMARY", "=" * 70]
    if not all_results or not all_results[0]:
        return "\n".join(lines + ["(no results)"])

    tools = [r.tool for r in all_results[0]]
    header = (
        "| Scenario | "
        + " | ".join(f"{t} (mean)" for t in tools)
        + " | Winner | Speedup |"
    )
    sep = "|" + "|".join("-" * (len(c) + 2) for c in header.split("|")[1:-1]) + "|"
    lines += ["\n" + header, sep]

    for scenario_results in all_results:
        means = {r.tool: r.mean for r in scenario_results}
        scenario_name = scenario_results[0].scenario
        winner = min(means, key=lambda k: means[k])
        runner_up = sorted(means.values())[1] if len(means) > 1 else means[winner]
        speedup = f"{runner_up / means[winner]:.1f}x" if means[winner] > 0 else "--"
        cells = " | ".join(f"{means[t]:.2f}s" for t in tools)
        lines.append(f"| {scenario_name} | {cells} | **{winner}** | {speedup} |")

    lines.append("\n## Detailed Results\n")
    for scenario_results in all_results:
        scenario_name = scenario_results[0].scenario
        lines.append(f"### {scenario_name}\n")
        for r in scenario_results:
            lines.append(f"**{r.tool}:**")
            lines.append(f"  - Mean: {r.mean:.2f}s (+/-{r.stddev:.2f}s)")
            lines.append(f"  - Range: {r.min:.2f}s -- {r.max:.2f}s")
            lines.append(f"  - Info: {r.cache_info}")
            lines.append("")
    return "\n".join(lines)


def export_json(all_results: list[list[BenchmarkResult]], output_path: Path) -> None:
    data = {
        "timestamp": datetime.now().isoformat(),
        "scenarios": [],
    }
    for scenario_results in all_results:
        entry: dict[str, object] = {"name": scenario_results[0].scenario}
        for r in scenario_results:
            entry[r.tool] = {
                "times": r.times,
                "mean": r.mean,
                "stddev": r.stddev,
                "cache_info": r.cache_info,
            }
        data["scenarios"].append(entry)
    with open(output_path, "w") as f:
        json.dump(data, f, indent=2)
    print(f"\nResults exported to: {output_path}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Benchmark incremental builds for go2nix modes."
    )
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument(
        "--scenario",
        choices=["no_change", *TOUCH_SCENARIOS.keys(), "all"],
        default="all",
    )
    parser.add_argument(
        "--touch-mode",
        choices=list(_TOUCH_TEMPLATES.keys()),
        default="private",
        help="Edit type: private=internal symbol, exported=API change (default: private)",
    )
    parser.add_argument(
        "--tools",
        default="nix,nix-ca",
        help="Comma-separated tools (default: nix,nix-ca)",
    )
    parser.add_argument("--json", type=Path, help="Export results as JSON")
    args = parser.parse_args()

    repo_root = get_repo_root()
    fixture_src = repo_root / "tests" / "fixtures" / "torture-project"
    go2nix_src = str(repo_root)

    print("Resolving dependencies...")
    nixpkgs_path, plugin_path, gomodcache = resolve_paths(repo_root)

    # Detect system
    _, system, _ = run_command(["nix", "eval", "--raw", "--impure", "--expr", "builtins.currentSystem"])
    system = system.strip()

    # Write nix expressions to a temp dir
    tmpdir = Path(os.environ.get("TMPDIR", "/tmp")) / "bench-incremental"
    tmpdir.mkdir(parents=True, exist_ok=True)

    # All builds use a local socat daemon with ca-derivations enabled.
    # This ensures a fair comparison: same store, same sandbox=false,
    # same nix binary with the plugin. Dependencies are fetched from the
    # system daemon store.
    local_daemon = LocalDaemon(tmpdir, "ca-derivations")
    local_daemon.start()
    common_opts = [
        "--option", "sandbox", "false",
        "--option", "substituters",
        "daemon https://cache.nixos.org https://cache.numtide.com",
        "--option", "trusted-public-keys",
        "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= "
        "cache.numtide.com-1:bf/T+iRCVpNgNStXCXTiUMsdMfEfhaQzExC9NG28oIg=",
    ]

    expr_nix = write_nix_expr(tmpdir, "nix", str(fixture_src), go2nix_src, system)
    expr_ca = write_nix_expr(tmpdir, "nix-ca", str(fixture_src), go2nix_src, system, "contentAddressed = true;")

    available_tools: dict[str, NixTool] = {
        "nix": NixTool("nix", nixpkgs_path, plugin_path, gomodcache, expr_nix,
                        common_opts, local_daemon),
        "nix-ca": NixTool("nix-ca", nixpkgs_path, plugin_path, gomodcache, expr_ca,
                           common_opts + ["--option", "extra-experimental-features", "ca-derivations"],
                           local_daemon),
    }

    requested = [t.strip() for t in args.tools.split(",")]
    tools: list[NixTool] = []
    for name in requested:
        if name in available_tools:
            tools.append(available_tools[name])
        else:
            parser.error(f"Unknown tool: {name!r} (available: {list(available_tools)})")

    # Copy fixture
    fixture_copy = Path(os.environ.get("TMPDIR", "/tmp")) / "bench-fixture-copy"
    if fixture_copy.exists():
        shutil.rmtree(fixture_copy)
    shutil.copytree(fixture_src, fixture_copy)

    print(f"\n{'=' * 70}")
    print("GO2NIX INCREMENTAL BUILD BENCHMARK")
    print(f"{'=' * 70}")
    print(f"Fixture:    torture-project/app-full")
    print(f"Tools:      {', '.join(t.name for t in tools)}")
    print(f"Touch mode: {args.touch_mode}")
    print(f"Runs:       {args.runs}")
    print(f"Store:      {local_daemon.store_root}")

    scenarios = (
        ["no_change", *TOUCH_SCENARIOS.keys()]
        if args.scenario == "all"
        else [args.scenario]
    )

    all_results: list[list[BenchmarkResult]] = []
    for name in scenarios:
        if name == "no_change":
            all_results.append(run_no_change_benchmark(tools, fixture_copy, args.runs))
        else:
            all_results.append(
                run_touch_benchmark(
                    tools, fixture_src, fixture_copy, name, args.touch_mode, args.runs
                )
            )

    print(format_results(all_results))

    if args.json:
        export_json(all_results, args.json)

    # Cleanup
    local_daemon.stop()
    shutil.rmtree(fixture_copy, ignore_errors=True)
    shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    main()
