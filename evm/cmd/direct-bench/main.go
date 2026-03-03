// direct-bench measures raw EVM execution performance with zero consensus overhead.
//
// Usage:
//
//	direct-bench [flags]
//	  --workload   Workload: transfer, erc20, storage, swap, mixed, all (default: all)
//	  --txs        Number of transactions per workload (default: 1000)
//	  --runs       Number of runs per workload for averaging (default: 3)
//	  --parallel   Also run parallel sig recovery comparison (default: false)
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/luxfi/benchmarks/evm/bench"
)

type workloadDef struct {
	name string
	gen  func(n int) ([]*types.Transaction, error)
}

func main() {
	workload := flag.String("workload", "all", "Workload: transfer, erc20, storage, swap, mixed, all")
	numTxs := flag.Int("txs", 1000, "Transactions per workload")
	numRuns := flag.Int("runs", 3, "Runs per workload for averaging")
	parallel := flag.Bool("parallel", false, "Run parallel sig recovery comparison")
	flag.Parse()

	allWorkloads := []workloadDef{
		{"ETH Transfer", bench.GenerateTransfers},
		{"ERC20 Transfer", bench.GenerateERC20Transfers},
		{"Storage Write", bench.GenerateStorageWrites},
		{"Uniswap Swap", bench.GenerateUniswapSwaps},
		{"Mixed DeFi", bench.GenerateMixedDeFi},
	}

	shortNames := map[string]string{
		"transfer": "ETH Transfer",
		"erc20":    "ERC20 Transfer",
		"storage":  "Storage Write",
		"swap":     "Uniswap Swap",
		"mixed":    "Mixed DeFi",
	}

	var workloads []workloadDef
	if *workload == "all" {
		workloads = allWorkloads
	} else if fullName, ok := shortNames[strings.ToLower(*workload)]; ok {
		for _, w := range allWorkloads {
			if w.name == fullName {
				workloads = append(workloads, w)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "Unknown workload: %s\n", *workload)
		fmt.Fprintf(os.Stderr, "Available: transfer, erc20, storage, swap, mixed, all\n")
		os.Exit(1)
	}

	cfg := bench.DefaultDirectConfig()

	type row struct {
		name     string
		txs      int
		gasPerTx uint64
		ds       bench.DirectStats
		runs     int
	}

	var rows []row

	for _, w := range workloads {
		n := *numTxs
		fmt.Fprintf(os.Stderr, "Running %s (%d txs, %d runs)...\n", w.name, n, *numRuns)

		gen := func() ([]*types.Transaction, error) {
			return w.gen(n)
		}
		results, err := bench.RunDirectMulti(gen, *numRuns, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			continue
		}

		ds := bench.ComputeStats(results)
		gasPerTx := results[0].AvgGasPerTx

		rows = append(rows, row{
			name:     w.name,
			txs:      results[0].TxCount,
			gasPerTx: gasPerTx,
			ds:       ds,
			runs:     *numRuns,
		})
	}

	// Print report.
	fmt.Println()
	fmt.Printf("%-18s | %-6s | %-14s | %-14s | %-14s | %-14s | %-14s\n",
		"Workload", "Txs", "Sig (ms)", "EVM (ms)", "Total (ms)", "Mgas/s (EVM)", "Mgas/s (Total)")
	fmt.Println(strings.Repeat("-", 112))
	for _, r := range rows {
		fmt.Printf("%-18s | %-6d | %-14s | %-14s | %-14s | %-14s | %-14s\n",
			r.name, r.txs,
			formatStat(r.ds.MeanSigMs, r.ds.StdSigMs),
			formatStat(r.ds.MeanEVMMs, r.ds.StdEVMMs),
			formatStat(r.ds.MeanTotalMs, r.ds.StdTotalMs),
			formatStat(r.ds.MeanMgasEVM, r.ds.StdMgasEVM),
			formatStat(r.ds.MeanMgasTotal, r.ds.StdMgasTotal))
	}
	fmt.Println()

	if !*parallel {
		return
	}

	// Parallel sig recovery comparison.
	fmt.Fprintf(os.Stderr, "\n--- Parallel Signature Recovery (GOMAXPROCS=%d) ---\n\n", runtime.GOMAXPROCS(0))

	type parRow struct {
		name string
		txs  int
		ps   bench.ParallelStats
	}

	var parRows []parRow

	for _, w := range workloads {
		n := *numTxs
		fmt.Fprintf(os.Stderr, "Running parallel comparison: %s (%d txs, %d runs)...\n", w.name, n, *numRuns)

		gen := func() ([]*types.Transaction, error) {
			return w.gen(n)
		}
		results, err := bench.RunParallelCompareMulti(gen, *numRuns, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			continue
		}

		ps := bench.ComputeParallelStats(results)
		parRows = append(parRows, parRow{
			name: w.name,
			txs:  results[0].TxCount,
			ps:   ps,
		})
	}

	fmt.Println()
	fmt.Printf("%-18s | %-6s | %-14s | %-14s | %-14s | %-14s\n",
		"Workload", "Txs", "Seq Sig (ms)", "Par Sig (ms)", "Speedup", "EVM (ms)")
	fmt.Println(strings.Repeat("-", 90))
	for _, r := range parRows {
		fmt.Printf("%-18s | %-6d | %-14s | %-14s | %-14s | %-14s\n",
			r.name, r.txs,
			formatStat(r.ps.MeanSeqSigMs, r.ps.StdSeqSigMs),
			formatStat(r.ps.MeanParSigMs, r.ps.StdParSigMs),
			formatStat(r.ps.MeanSpeedup, r.ps.StdSpeedup),
			formatStat(r.ps.MeanEVMMs, r.ps.StdEVMMs))
	}
	fmt.Println()
}

func formatStat(mean, std float64) string {
	if std == 0 || math.IsNaN(std) {
		return fmt.Sprintf("%.1f", mean)
	}
	return fmt.Sprintf("%.1f +/- %.1f", mean, std)
}
