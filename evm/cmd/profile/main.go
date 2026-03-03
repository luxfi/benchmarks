package main

import (
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/luxfi/benchmarks/evm/bench"
)

func main() {
	f, _ := os.Create("/tmp/evm_cpu.prof")
	pprof.StartCPUProfile(f)

	txs, err := bench.GenerateTransfers(10000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
	result, err := bench.RunDirect(txs, nil)

	pprof.StopCPUProfile()
	f.Close()

	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%.1f Mgas/s in %.1fms\n", float64(result.TotalGas)/result.Duration.Seconds()/1e6, float64(result.Duration.Milliseconds()))
}
