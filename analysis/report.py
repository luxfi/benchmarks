#!/usr/bin/env python3
"""Generate markdown benchmark report."""

import json
from pathlib import Path
from datetime import datetime


def load_results(results_dir: Path) -> dict:
    """Load all benchmark results from a directory."""
    results = {}
    for f in results_dir.glob("*.json"):
        with open(f) as fp:
            data = json.load(fp)
            if isinstance(data, list):
                for item in data:
                    chain = item.get("chain", "unknown")
                    if chain not in results:
                        results[chain] = {}
                    bench = item.get("benchmark", "unknown")
                    results[chain][bench] = item.get("metrics", {})
    return results


def generate_report(results: dict, output_path: Path):
    """Generate markdown report."""
    lines = [
        "# Lux Blockchain Benchmark Report",
        "",
        f"**Generated**: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}",
        "",
        "## Summary",
        "",
        "| Chain | TPS | P50 Latency | P99 Latency | Avg Memory |",
        "|-------|-----|-------------|-------------|------------|",
    ]

    for chain, metrics in sorted(results.items()):
        tps = metrics.get("tps", {}).get("tps", "N/A")
        p50 = metrics.get("latency", {}).get("p50", "N/A")
        p99 = metrics.get("latency", {}).get("p99", "N/A")
        mem = metrics.get("memory", {}).get("avg_mb", "N/A")

        tps_str = f"{tps:.0f}" if isinstance(tps, (int, float)) else tps
        p50_str = f"{p50:.1f}ms" if isinstance(p50, (int, float)) else p50
        p99_str = f"{p99:.1f}ms" if isinstance(p99, (int, float)) else p99
        mem_str = f"{mem:.0f}MB" if isinstance(mem, (int, float)) else mem

        lines.append(f"| {chain} | {tps_str} | {p50_str} | {p99_str} | {mem_str} |")

    lines.extend([
        "",
        "## TPS Comparison",
        "",
        "![TPS Comparison](charts/tps_comparison.png)",
        "",
        "## Latency Comparison",
        "",
        "![Latency Comparison](charts/latency_comparison.png)",
        "",
        "## Memory Usage",
        "",
        "![Memory Comparison](charts/memory_comparison.png)",
        "",
        "## Methodology",
        "",
        "All benchmarks run with:",
        "- 5-node networks",
        "- Identical hardware limits (2 CPU, 4GB RAM per node)",
        "- Docker networking (same latency)",
        "- 60-second measurement windows",
        "",
        "## Notes",
        "",
        "- TPS measured as successful transactions over benchmark duration",
        "- Latency measured from transaction send to receipt confirmation",
        "- Memory sampled every second via Docker stats",
        "",
    ])

    report = "\n".join(lines)
    output_path.write_text(report)
    print(f"Report saved to: {output_path}")


def main():
    results_base = Path(__file__).parent.parent / "results"

    if not results_base.exists():
        print("No results directory found.")
        return

    date_dirs = sorted([d for d in results_base.iterdir() if d.is_dir() and d.name != "charts"])
    if not date_dirs:
        print("No benchmark results found.")
        return

    results_dir = date_dirs[-1]
    print(f"Loading results from: {results_dir}")

    results = load_results(results_dir)
    if not results:
        print("No results to report.")
        return

    output_path = results_base / "REPORT.md"
    generate_report(results, output_path)


if __name__ == "__main__":
    main()
