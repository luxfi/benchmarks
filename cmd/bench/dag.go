// Copyright (C) 2025-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luxfi/benchmarks/benchmarks"
)

func runDAG(args []string) {
	fs := flag.NewFlagSet("dag", flag.ExitOnError)
	seed := fs.Int64("seed", 42, "deterministic PRNG seed")
	numProofs := fs.Int("proofs", 100, "number of ZK proofs for Z-Chain bench")
	numTxs := fs.Int("txs", 1000, "number of EVM transactions")
	runs := fs.Int("runs", 3, "number of runs per benchmark for averaging")
	workers := fs.Int("workers", 0, "parallel workers (0=GOMAXPROCS)")
	vertexSize := fs.Int("vertex-size", 0, "max txs/proofs per DAG vertex (0=auto)")
	workload := fs.String("workload", "all", "EVM workload: erc20, amm, arb, random, all")
	mode := fs.String("mode", "all", "execution mode: linear, block-stm, dag-cpu, dag-gpu, z-linear, z-dag, all")
	format := fs.String("format", "markdown", "output format: markdown, json, both")
	outputDir := fs.String("output", "", "write results to directory (empty=stdout)")
	fs.Parse(args)

	fmt.Fprintf(os.Stderr, "=== DAG Benchmark Suite ===\n")
	fmt.Fprintf(os.Stderr, "Seed: %d | Txs: %d | Proofs: %d | Runs: %d | Workers: %d\n\n",
		*seed, *numTxs, *numProofs, *runs, *workers)

	runEVM := *mode == "all" || isEVMMode(*mode)
	runZChain := *mode == "all" || isZChainMode(*mode)

	// Z-Chain DAG benchmark.
	if runZChain {
		fmt.Fprintf(os.Stderr, "--- Z-Chain DAG Benchmark ---\n")
		zCfg := &benchmarks.ZChainBenchConfig{
			Seed:       *seed,
			NumProofs:  *numProofs,
			Runs:       *runs,
			Workers:    *workers,
			VertexSize: *vertexSize,
		}

		start := time.Now()
		zReport, err := benchmarks.RunZChainMatrix(zCfg)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Z-Chain benchmark failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Z-Chain complete in %v\n\n", elapsed)

		if *format == "markdown" || *format == "both" {
			md := benchmarks.FormatZChainMarkdownTable(zReport)
			writeOutput(*outputDir, "dag_zchain.md", md)
			fmt.Print(md)
		}
		if *format == "json" || *format == "both" {
			jsonStr, err := benchmarks.FormatZChainJSON(zReport)
			if err != nil {
				fmt.Fprintf(os.Stderr, "JSON encoding failed: %v\n", err)
				os.Exit(1)
			}
			writeOutput(*outputDir, "dag_zchain.json", jsonStr)
			if *format == "json" {
				fmt.Print(jsonStr)
			}
		}
	}

	// EVM DAG benchmark (stub -- uses evm-bench package).
	// The EVM bench lives in github.com/luxfi/evm-bench, not this module.
	// Print instructions for running the EVM DAG bench.
	if runEVM {
		fmt.Fprintf(os.Stderr, "--- DAG EVM Benchmark ---\n")
		fmt.Fprintf(os.Stderr, "EVM DAG benchmarks live in github.com/luxfi/evm-bench.\n")
		fmt.Fprintf(os.Stderr, "Run: cd ~/work/lux/evm-bench && go test -v -run TestRunDAGBench ./bench/ -count=1\n")
		fmt.Fprintf(os.Stderr, "Or:  cd ~/work/lux/evm-bench && go test -v -run TestRunDAGBenchMatrix ./bench/ -count=1\n")
		fmt.Fprintf(os.Stderr, "\nWorkloads: erc20 (low conflict), amm (medium), arb (high), random (synthetic)\n")
		fmt.Fprintf(os.Stderr, "Modes: linear, block-stm, dag-cpu, dag-gpu\n")

		if *workload != "all" {
			fmt.Fprintf(os.Stderr, "\nFiltered workload: %s\n", *workload)
		}
	}
}

func isEVMMode(m string) bool {
	switch m {
	case "linear", "block-stm", "dag-cpu", "dag-gpu":
		return true
	}
	return false
}

func isZChainMode(m string) bool {
	switch m {
	case "z-linear", "z-dag":
		return true
	}
	return false
}

func writeOutput(dir, filename, content string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", dir, err)
		return
	}
	path := dir + "/" + filename
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
		return
	}
	fmt.Fprintf(os.Stderr, "Wrote %s (%d bytes)\n", path, len(content))
}

func dagUsage() string {
	return `  dag       DAG EVM + Z-Chain benchmark suite
            Options:
              --seed N         Deterministic PRNG seed (default: 42)
              --txs N          EVM transactions per workload (default: 1000)
              --proofs N       ZK proofs for Z-Chain (default: 100)
              --runs N         Runs per benchmark (default: 3)
              --workers N      Parallel workers, 0=auto (default: 0)
              --vertex-size N  Max items per vertex, 0=auto (default: 0)
              --workload W     erc20, amm, arb, random, all (default: all)
              --mode M         linear, block-stm, dag-cpu, dag-gpu, z-linear, z-dag, all
              --format F       markdown, json, both (default: markdown)
              --output DIR     Write results to directory (default: stdout)`
}

// Suppress unused import warnings.
var _ = strings.TrimSpace
