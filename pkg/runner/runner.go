// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luxfi/benchmarks/pkg/chain"
	"github.com/luxfi/benchmarks/pkg/metrics"
)

type Config struct {
	Duration    time.Duration
	Workload    string
	Chains      string
	Concurrency int
	Output      string
}

func (c Config) ChainList() []string {
	return strings.Split(c.Chains, ",")
}

type Result struct {
	Chain     string         `json:"chain"`
	Benchmark string         `json:"benchmark"`
	Timestamp time.Time      `json:"timestamp"`
	Duration  time.Duration  `json:"duration"`
	Metrics   map[string]any `json:"metrics"`
}

func RunTPS(ctx context.Context, cfg Config) error {
	fmt.Println("=== TPS Benchmark ===")
	fmt.Printf("Duration: %s, Workload: %s, Concurrency: %d\n\n", cfg.Duration, cfg.Workload, cfg.Concurrency)

	for _, chainName := range cfg.ChainList() {
		chainName = strings.TrimSpace(chainName)
		c, err := chain.Get(chainName)
		if err != nil {
			fmt.Printf("⚠ Skipping %s: %v\n", chainName, err)
			continue
		}

		fmt.Printf("Benchmarking %s...\n", chainName)

		collector := metrics.NewCollector()

		if err := c.Connect(ctx); err != nil {
			fmt.Printf("⚠ Failed to connect to %s: %v\n", chainName, err)
			continue
		}

		start := time.Now()
		txCount, err := c.SendTransactions(ctx, cfg.Duration, cfg.Concurrency, cfg.Workload, collector)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("⚠ Error during benchmark: %v\n", err)
		}

		tps := float64(txCount) / elapsed.Seconds()
		fmt.Printf("  ✓ %s: %.2f TPS (%d txs in %s)\n\n", chainName, tps, txCount, elapsed.Round(time.Millisecond))

		c.Disconnect()
	}

	return nil
}

func RunLatency(ctx context.Context, cfg Config) error {
	fmt.Println("=== Latency Benchmark ===")
	fmt.Printf("Duration: %s, Workload: %s\n\n", cfg.Duration, cfg.Workload)

	for _, chainName := range cfg.ChainList() {
		chainName = strings.TrimSpace(chainName)
		c, err := chain.Get(chainName)
		if err != nil {
			fmt.Printf("⚠ Skipping %s: %v\n", chainName, err)
			continue
		}

		fmt.Printf("Benchmarking %s...\n", chainName)

		if err := c.Connect(ctx); err != nil {
			fmt.Printf("⚠ Failed to connect to %s: %v\n", chainName, err)
			continue
		}

		latencies, err := c.MeasureLatency(ctx, cfg.Duration, cfg.Workload)
		if err != nil {
			fmt.Printf("⚠ Error during benchmark: %v\n", err)
			c.Disconnect()
			continue
		}

		stats := metrics.LatencyStats(latencies)
		fmt.Printf("  ✓ %s: P50=%.2fms P95=%.2fms P99=%.2fms\n\n",
			chainName, stats.P50, stats.P95, stats.P99)

		c.Disconnect()
	}

	return nil
}

func RunMemory(ctx context.Context, cfg Config) error {
	fmt.Println("=== Memory Benchmark ===")
	fmt.Printf("Duration: %s\n\n", cfg.Duration)

	for _, chainName := range cfg.ChainList() {
		chainName = strings.TrimSpace(chainName)
		c, err := chain.Get(chainName)
		if err != nil {
			fmt.Printf("⚠ Skipping %s: %v\n", chainName, err)
			continue
		}

		fmt.Printf("Benchmarking %s...\n", chainName)

		stats, err := c.MeasureMemory(ctx, cfg.Duration)
		if err != nil {
			fmt.Printf("⚠ Error: %v\n", err)
			continue
		}

		fmt.Printf("  ✓ %s: Avg=%.2f MB, Peak=%.2f MB\n\n",
			chainName, float64(stats.Avg)/(1024*1024), float64(stats.Peak)/(1024*1024))
	}

	return nil
}

func RunQuery(ctx context.Context, cfg Config) error {
	fmt.Println("=== Query Performance Benchmark ===")
	fmt.Printf("Duration: %s, Concurrency: %d\n\n", cfg.Duration, cfg.Concurrency)

	for _, chainName := range cfg.ChainList() {
		chainName = strings.TrimSpace(chainName)
		c, err := chain.Get(chainName)
		if err != nil {
			fmt.Printf("⚠ Skipping %s: %v\n", chainName, err)
			continue
		}

		fmt.Printf("Benchmarking %s...\n", chainName)

		if err := c.Connect(ctx); err != nil {
			fmt.Printf("⚠ Failed to connect to %s: %v\n", chainName, err)
			continue
		}

		stats, err := c.MeasureQueryPerformance(ctx, cfg.Duration, cfg.Concurrency)
		if err != nil {
			fmt.Printf("⚠ Error: %v\n", err)
			c.Disconnect()
			continue
		}

		fmt.Printf("  ✓ %s: %.2f queries/sec, Avg=%.2fms\n\n",
			chainName, stats.QPS, stats.AvgLatency)

		c.Disconnect()
	}

	return nil
}

func RunAll(ctx context.Context, cfg Config) error {
	fmt.Println("╔═══════════════════════════════════════╗")
	fmt.Println("║   Lux Blockchain Benchmark Suite      ║")
	fmt.Println("╚═══════════════════════════════════════╝")
	fmt.Println()

	results := make([]Result, 0)

	// TPS
	cfg.Concurrency = 10
	cfg.Workload = "raw"
	if err := RunTPS(ctx, cfg); err != nil {
		return err
	}

	// Latency
	if err := RunLatency(ctx, cfg); err != nil {
		return err
	}

	// Memory
	if err := RunMemory(ctx, cfg); err != nil {
		return err
	}

	// Query
	cfg.Concurrency = 50
	if err := RunQuery(ctx, cfg); err != nil {
		return err
	}

	// Save results
	if cfg.Output != "" {
		if err := os.MkdirAll(cfg.Output, 0755); err != nil {
			return err
		}

		data, _ := json.MarshalIndent(results, "", "  ")
		outPath := filepath.Join(cfg.Output, "results.json")
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return err
		}
		fmt.Printf("\nResults saved to %s\n", outPath)
	}

	return nil
}
