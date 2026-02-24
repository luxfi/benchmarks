// Copyright (C) 2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build cgo

package bench

import (
	"fmt"
	"runtime"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/luxfi/cevm"
)

// runCEVM executes the workload through luxfi/cevm (the C++ EVM via CGo).
// backend selects sequential, Block-STM, or GPU dispatch.
func runCEVM(
	mode string,
	backend cevm.Backend,
	txs []*types.Transaction,
	vertices []DAGVertex,
	workload string,
	conflictRate float64,
	concurrency int,
	cfg *DAGBenchConfig,
) (*DAGResult, error) {
	directCfg := DefaultDirectConfig()
	signer := types.LatestSignerForChainID(directCfg.ChainConfig.ChainID)

	senders, sigDuration, err := recoverSendersParallel(txs, signer)
	if err != nil {
		return nil, err
	}
	_ = senders

	cevmTxs := make([]cevm.Transaction, len(txs))
	for i, tx := range txs {
		from, err := types.Sender(signer, tx)
		if err != nil {
			return nil, fmt.Errorf("tx %d: recover sender: %w", i, err)
		}
		copy(cevmTxs[i].From[:], from[:])
		if to := tx.To(); to != nil {
			copy(cevmTxs[i].To[:], to[:])
			cevmTxs[i].HasTo = true
		}
		cevmTxs[i].Data = tx.Data()
		cevmTxs[i].GasLimit = tx.Gas()
		cevmTxs[i].Nonce = tx.Nonce()
		if v := tx.Value(); v != nil && v.IsUint64() {
			cevmTxs[i].Value = v.Uint64()
		}
		if g := tx.GasPrice(); g != nil && g.IsUint64() {
			cevmTxs[i].GasPrice = g.Uint64()
		}
	}

	start := time.Now()
	result, err := cevm.ExecuteBlock(backend, cevmTxs)
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("cevm execute: %w", err)
	}

	latencies := make([]time.Duration, len(txs))
	if len(txs) > 0 {
		per := elapsed / time.Duration(len(txs))
		for i := range latencies {
			latencies[i] = per
		}
	}

	speedup := 1.0
	if backend == cevm.CPUParallel || backend == cevm.GPUMetal || backend == cevm.GPUCUDA {
		speedup = float64(concurrency)
		if speedup < 1 {
			speedup = float64(runtime.GOMAXPROCS(0))
		}
	}

	return buildDAGResult(
		mode, workload, txs, vertices,
		result.TotalGas, sigDuration, elapsed, latencies,
		conflictRate, speedup,
	), nil
}

// CEVMAvailable reports whether cevm execution is wired in this build.
// True for cgo builds, false otherwise.
func CEVMAvailable() bool { return true }

// CEVMBackend reports the auto-detected backend that cevm would use.
func CEVMBackend() string { return cevm.AutoDetect().String() }

// (kept to avoid unused-import warnings if helper signatures change)
var _ = common.Address{}

func runCEVMSeq(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	return runCEVM(ModeCEVMSeq, cevm.CPUSequential, txs, vertices, workload, conflictRate, concurrency, cfg)
}

func runCEVMPar(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	return runCEVM(ModeCEVMPar, cevm.CPUParallel, txs, vertices, workload, conflictRate, concurrency, cfg)
}

func runCEVMGPU(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	return runCEVM(ModeCEVMGPU, cevm.AutoDetect(), txs, vertices, workload, conflictRate, concurrency, cfg)
}
