// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/luxfi/benchmarks/pkg/runner"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "tps":
		runTPS(args)
	case "latency":
		runLatency(args)
	case "memory":
		runMemory(args)
	case "query":
		runQuery(args)
	case "all":
		runAll(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`Lux Blockchain Benchmarks

Usage: bench <command> [options]

Commands:
  tps       Measure transactions per second
  latency   Measure transaction confirmation latency
  memory    Measure node memory usage under load
  query     Measure RPC query performance
  all       Run all benchmarks

Options:
  --duration    Benchmark duration (default: 60s)
  --workload    Workload type: raw, erc20, uniswap, nft (default: raw)
  --chains      Chains to benchmark: lux,avalanche,geth,op,solana (default: all)
  --output      Output directory for results (default: results/)
  --concurrency Number of concurrent workers (default: 10)`)
}

func runTPS(args []string) {
	fs := flag.NewFlagSet("tps", flag.ExitOnError)
	duration := fs.Duration("duration", 60*time.Second, "benchmark duration")
	workload := fs.String("workload", "raw", "workload type")
	chains := fs.String("chains", "lux,avalanche,geth,op,solana", "chains to benchmark")
	concurrency := fs.Int("concurrency", 10, "concurrent workers")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := runner.Config{
		Duration:    *duration,
		Workload:    *workload,
		Chains:      *chains,
		Concurrency: *concurrency,
	}

	if err := runner.RunTPS(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runLatency(args []string) {
	fs := flag.NewFlagSet("latency", flag.ExitOnError)
	duration := fs.Duration("duration", 60*time.Second, "benchmark duration")
	workload := fs.String("workload", "raw", "workload type")
	chains := fs.String("chains", "lux,avalanche,geth,op,solana", "chains to benchmark")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := runner.Config{
		Duration: *duration,
		Workload: *workload,
		Chains:   *chains,
	}

	if err := runner.RunLatency(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runMemory(args []string) {
	fs := flag.NewFlagSet("memory", flag.ExitOnError)
	duration := fs.Duration("duration", 60*time.Second, "benchmark duration")
	chains := fs.String("chains", "lux,avalanche,geth,op,solana", "chains to benchmark")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := runner.Config{
		Duration: *duration,
		Chains:   *chains,
	}

	if err := runner.RunMemory(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runQuery(args []string) {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	duration := fs.Duration("duration", 60*time.Second, "benchmark duration")
	chains := fs.String("chains", "lux,avalanche,geth,op,solana", "chains to benchmark")
	concurrency := fs.Int("concurrency", 50, "concurrent workers")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := runner.Config{
		Duration:    *duration,
		Chains:      *chains,
		Concurrency: *concurrency,
	}

	if err := runner.RunQuery(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runAll(args []string) {
	fs := flag.NewFlagSet("all", flag.ExitOnError)
	duration := fs.Duration("duration", 60*time.Second, "benchmark duration per test")
	output := fs.String("output", "results/", "output directory")
	chains := fs.String("chains", "lux,avalanche,geth,op,solana", "chains to benchmark")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := runner.Config{
		Duration: *duration,
		Output:   *output,
		Chains:   *chains,
	}

	if err := runner.RunAll(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
