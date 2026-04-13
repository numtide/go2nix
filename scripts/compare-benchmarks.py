#!/usr/bin/env python3
"""Compare two bench-incremental JSON outputs and flag regressions.

Reads baseline and current JSON files produced by bench-incremental,
compares mean wall-clock times and derivation build counts per
scenario+tool pair, and outputs a GitHub-flavoured Markdown table.

Exits 0 if no regressions, 1 if any scenario regressed.
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from pathlib import Path


@dataclass
class ToolStats:
    mean: float
    builds_mean: float


@dataclass
class Comparison:
    scenario: str
    tool: str
    base: ToolStats
    current: ToolStats

    @property
    def time_pct_change(self) -> float:
        if self.base.mean == 0:
            return 0.0
        return ((self.current.mean - self.base.mean) / self.base.mean) * 100

    @property
    def drv_delta(self) -> float:
        return self.current.builds_mean - self.base.builds_mean

    def is_regression(self, threshold: float) -> tuple[bool, str]:
        reasons: list[str] = []
        if self.drv_delta > 0:
            reasons.append("drvs")
        if self.time_pct_change > threshold:
            reasons.append("time")
        if reasons:
            return True, ", ".join(reasons)
        return False, ""


def load_results(path: Path) -> dict[str, dict[str, ToolStats]]:
    """Parse bench-incremental JSON into {scenario: {tool: ToolStats}}."""
    data = json.loads(path.read_text())
    out: dict[str, dict[str, ToolStats]] = {}
    for scenario in data["scenarios"]:
        name = scenario["name"]
        tools: dict[str, ToolStats] = {}
        for tool_name, tool_data in scenario["tools"].items():
            tools[tool_name] = ToolStats(
                mean=tool_data["mean"],
                builds_mean=tool_data["builds_mean"],
            )
        out[name] = tools
    return out


def compare(
    baseline: dict[str, dict[str, ToolStats]],
    current: dict[str, dict[str, ToolStats]],
) -> list[Comparison]:
    """Produce comparisons for all scenario+tool pairs present in both."""
    comparisons: list[Comparison] = []
    for scenario in baseline:
        if scenario not in current:
            continue
        for tool in baseline[scenario]:
            if tool not in current[scenario]:
                continue
            comparisons.append(
                Comparison(
                    scenario=scenario,
                    tool=tool,
                    base=baseline[scenario][tool],
                    current=current[scenario][tool],
                )
            )
    return comparisons


def format_change(pct: float) -> str:
    sign = "+" if pct >= 0 else ""
    return f"{sign}{pct:.1f}%"


def format_table(comparisons: list[Comparison], threshold: float) -> str:
    lines: list[str] = []
    lines.append(
        "| Scenario | Tool | Base (s) | Current (s) | Change "
        "| Drvs (base) | Drvs (curr) | Status |"
    )
    lines.append("|:---|:---|---:|---:|---:|---:|---:|:---|")
    for c in comparisons:
        is_reg, reason = c.is_regression(threshold)
        status = f"REGRESSION ({reason})" if is_reg else "ok"
        lines.append(
            f"| {c.scenario} | {c.tool} "
            f"| {c.base.mean:.2f} | {c.current.mean:.2f} "
            f"| {format_change(c.time_pct_change)} "
            f"| {c.base.builds_mean:.0f} | {c.current.builds_mean:.0f} "
            f"| {status} |"
        )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Compare bench-incremental JSON outputs for regressions."
    )
    parser.add_argument("baseline", type=Path, help="Baseline JSON (e.g. from main)")
    parser.add_argument("current", type=Path, help="Current JSON (e.g. from PR)")
    parser.add_argument(
        "--threshold",
        type=float,
        default=20.0,
        help="Wall-clock regression threshold in percent (default: 20)",
    )
    args = parser.parse_args()

    baseline = load_results(args.baseline)
    current = load_results(args.current)
    comparisons = compare(baseline, current)

    if not comparisons:
        print("No matching scenario+tool pairs found between baseline and current.")
        return 1

    table = format_table(comparisons, args.threshold)
    print(table)

    regressions = [c for c in comparisons if c.is_regression(args.threshold)[0]]
    if regressions:
        print(
            f"\n{len(regressions)} regression(s) detected "
            f"(threshold: {args.threshold}%)",
            file=sys.stderr,
        )
        return 1

    print("\nNo regressions detected.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
