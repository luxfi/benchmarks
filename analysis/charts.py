#!/usr/bin/env python3
"""Generate benchmark comparison charts."""

import json
import os
from pathlib import Path
from datetime import datetime

try:
    import matplotlib.pyplot as plt
    import numpy as np
except ImportError:
    print("Install dependencies: pip install matplotlib numpy")
    exit(1)


def load_results(results_dir: Path) -> dict:
    """Load all benchmark results from a directory."""
    results = {}
    for f in results_dir.glob("*.json"):
        with open(f) as fp:
            data = json.load(fp)
            if isinstance(data, list):
                for item in data:
                    chain = item.get("chain", "unknown")
                    bench = item.get("benchmark", "unknown")
                    if chain not in results:
                        results[chain] = {}
                    results[chain][bench] = item.get("metrics", {})
    return results


def plot_tps_comparison(results: dict, output_dir: Path):
    """Plot TPS comparison bar chart."""
    chains = list(results.keys())
    tps_values = [results[c].get("tps", {}).get("tps", 0) for c in chains]

    fig, ax = plt.subplots(figsize=(10, 6))
    bars = ax.bar(chains, tps_values, color=['#3498db', '#e74c3c', '#2ecc71', '#9b59b6', '#f39c12'])

    ax.set_ylabel('Transactions per Second')
    ax.set_title('TPS Comparison Across Blockchains')
    ax.set_ylim(0, max(tps_values) * 1.2 if tps_values else 100)

    # Add value labels on bars
    for bar, val in zip(bars, tps_values):
        ax.text(bar.get_x() + bar.get_width()/2, bar.get_height() + 50,
                f'{val:.0f}', ha='center', va='bottom', fontsize=10)

    plt.tight_layout()
    plt.savefig(output_dir / 'tps_comparison.png', dpi=150)
    plt.close()
    print(f"Saved: {output_dir / 'tps_comparison.png'}")


def plot_latency_comparison(results: dict, output_dir: Path):
    """Plot latency percentiles comparison."""
    chains = list(results.keys())

    p50 = [results[c].get("latency", {}).get("p50", 0) for c in chains]
    p95 = [results[c].get("latency", {}).get("p95", 0) for c in chains]
    p99 = [results[c].get("latency", {}).get("p99", 0) for c in chains]

    x = np.arange(len(chains))
    width = 0.25

    fig, ax = plt.subplots(figsize=(12, 6))
    ax.bar(x - width, p50, width, label='P50', color='#3498db')
    ax.bar(x, p95, width, label='P95', color='#e74c3c')
    ax.bar(x + width, p99, width, label='P99', color='#2ecc71')

    ax.set_ylabel('Latency (ms)')
    ax.set_title('Transaction Latency Comparison')
    ax.set_xticks(x)
    ax.set_xticklabels(chains)
    ax.legend()

    plt.tight_layout()
    plt.savefig(output_dir / 'latency_comparison.png', dpi=150)
    plt.close()
    print(f"Saved: {output_dir / 'latency_comparison.png'}")


def plot_memory_comparison(results: dict, output_dir: Path):
    """Plot memory usage comparison."""
    chains = list(results.keys())

    avg_mem = [results[c].get("memory", {}).get("avg_mb", 0) for c in chains]
    peak_mem = [results[c].get("memory", {}).get("peak_mb", 0) for c in chains]

    x = np.arange(len(chains))
    width = 0.35

    fig, ax = plt.subplots(figsize=(10, 6))
    ax.bar(x - width/2, avg_mem, width, label='Average', color='#3498db')
    ax.bar(x + width/2, peak_mem, width, label='Peak', color='#e74c3c')

    ax.set_ylabel('Memory (MB)')
    ax.set_title('Node Memory Usage Comparison')
    ax.set_xticks(x)
    ax.set_xticklabels(chains)
    ax.legend()

    plt.tight_layout()
    plt.savefig(output_dir / 'memory_comparison.png', dpi=150)
    plt.close()
    print(f"Saved: {output_dir / 'memory_comparison.png'}")


def main():
    # Find most recent results
    results_base = Path(__file__).parent.parent / "results"

    if not results_base.exists():
        print("No results directory found. Run benchmarks first.")
        return

    # Get most recent date directory
    date_dirs = sorted([d for d in results_base.iterdir() if d.is_dir() and d.name != "charts"])
    if not date_dirs:
        print("No benchmark results found.")
        return

    results_dir = date_dirs[-1]
    print(f"Loading results from: {results_dir}")

    results = load_results(results_dir)
    if not results:
        print("No results to plot.")
        return

    # Create charts directory
    charts_dir = results_base / "charts"
    charts_dir.mkdir(exist_ok=True)

    # Generate charts
    plot_tps_comparison(results, charts_dir)
    plot_latency_comparison(results, charts_dir)
    plot_memory_comparison(results, charts_dir)

    print(f"\nCharts saved to: {charts_dir}")


if __name__ == "__main__":
    main()
