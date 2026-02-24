// Copyright (C) 2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build !cgo

package bench

import (
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"
)

// cevmBackend is a no-op stand-in when CGo is disabled.
type cevmBackend int

const (
	cevmCPUSeq cevmBackend = 0
	cevmCPUPar cevmBackend = 1
	cevmGPU    cevmBackend = 2
)

func runCEVM(
	mode string,
	_ cevmBackend,
	_ []*types.Transaction,
	_ []DAGVertex,
	_ string,
	_ float64,
	_ int,
	_ *DAGBenchConfig,
) (*DAGResult, error) {
	return nil, fmt.Errorf("cevm bench mode %q requires CGO_ENABLED=1 + libevm built (luxcpp/evm)", mode)
}

func CEVMAvailable() bool { return false }
func CEVMBackend() string { return "n/a (no cgo)" }

func runCEVMSeq(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	return runCEVM(ModeCEVMSeq, cevmCPUSeq, txs, vertices, workload, conflictRate, concurrency, cfg)
}

func runCEVMPar(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	return runCEVM(ModeCEVMPar, cevmCPUPar, txs, vertices, workload, conflictRate, concurrency, cfg)
}

func runCEVMGPU(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	return runCEVM(ModeCEVMGPU, cevmGPU, txs, vertices, workload, conflictRate, concurrency, cfg)
}
