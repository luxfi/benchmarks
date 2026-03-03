// evm-bench benchmarks Lux EVM implementations.
//
// Usage:
//
//	evm-bench [flags]
//	  --lux-rpc    Lux EVM RPC endpoint (default: http://127.0.0.1:9660/ext/bc/C/rpc)
//	  --gpu-rpc    GPU EVM RPC endpoint (default: http://127.0.0.1:9670/ext/bc/C/rpc)
//	  --reth-rpc   Hanzo reth EVM RPC endpoint (default: http://127.0.0.1:9680/ext/bc/C/rpc)
//	  --txs        Number of transactions per workload (default: 100)
//	  --workload   Workload to run: transfer, erc20, storage, all (default: all)
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/luxfi/benchmarks/evm/bench"
)

func main() {
	luxRPC := flag.String("lux-rpc", "http://127.0.0.1:9660/ext/bc/C/rpc", "Lux EVM (Go, sequential)")
	gpuRPC := flag.String("gpu-rpc", "", "GPU EVM (Go, parallel Block-STM)")
	rethRPC := flag.String("reth-rpc", "", "Hanzo EVM (Rust, reth/revm)")
	numTxs := flag.Int("txs", 100, "Transactions per workload")
	workload := flag.String("workload", "all", "Workload: transfer, erc20, storage, all")
	chainID := flag.Int64("chain-id", 31337, "Chain ID")
	gpuChainID := flag.Int64("gpu-chain-id", 200200, "GPU EVM chain ID")
	rethChainID := flag.Int64("reth-chain-id", 200300, "Hanzo reth EVM chain ID")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	key := bench.DefaultKey()
	cid := big.NewInt(*chainID)

	type target struct {
		label   string
		rpc     string
		chainID *big.Int
	}
	targets := []target{
		{"lux-evm", *luxRPC, cid},
	}
	if *gpuRPC != "" {
		targets = append(targets, target{"gpu-evm", *gpuRPC, big.NewInt(*gpuChainID)})
	}
	if *rethRPC != "" {
		targets = append(targets, target{"reth-evm", *rethRPC, big.NewInt(*rethChainID)})
	}

	type workloadDef struct {
		name string
		fn   func(context.Context, *bench.Config) (*bench.Result, error)
	}

	workloads := []workloadDef{
		{"ETH Transfer", func(ctx context.Context, cfg *bench.Config) (*bench.Result, error) {
			cfg2 := *cfg
			return bench.Run(ctx, bench.Config{RPC: cfg2.RPC, Label: cfg2.Label, ChainID: cfg2.ChainID, Key: cfg2.Key, NumTxs: cfg2.NumTxs},
				bench.TransferWorkload)
		}},
		{"ERC20 Transfer", func(ctx context.Context, cfg *bench.Config) (*bench.Result, error) {
			return bench.Run(ctx, *cfg, bench.ERC20Workload)
		}},
		{"Storage Write", func(ctx context.Context, cfg *bench.Config) (*bench.Result, error) {
			return bench.Run(ctx, *cfg, bench.StorageWorkload)
		}},
	}

	if *workload != "all" {
		filtered := make([]workloadDef, 0)
		for _, w := range workloads {
			if strings.Contains(strings.ToLower(w.name), strings.ToLower(*workload)) {
				filtered = append(filtered, w)
			}
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "Unknown workload: %s\n", *workload)
			os.Exit(1)
		}
		workloads = filtered
	}

	var results []*bench.Result

	for _, t := range targets {
		fmt.Printf("Benchmarking %s (%s) with %d txs...\n", t.label, t.rpc, *numTxs)
		cfg := &bench.Config{
			RPC:     t.rpc,
			Label:   t.label,
			ChainID: t.chainID,
			Key:     key,
			NumTxs:  *numTxs,
		}

		for _, w := range workloads {
			fmt.Printf("  Running %s...", w.name)
			r, err := w.fn(ctx, cfg)
			if err != nil {
				fmt.Printf(" ERROR: %v\n", err)
				continue
			}
			r.Workload = w.name
			results = append(results, r)
			fmt.Printf(" %.1f TPS (%.2f Mgas/s)\n", r.TPS, r.MgasPerS)
		}
	}

	bench.PrintReport(results)
}
