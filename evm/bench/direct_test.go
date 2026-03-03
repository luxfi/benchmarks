package bench

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

func TestGenerateTransfers(t *testing.T) {
	txs, err := GenerateTransfers(10)
	if err != nil {
		t.Fatalf("GenerateTransfers: %v", err)
	}
	if len(txs) != 10 {
		t.Fatalf("expected 10 txs, got %d", len(txs))
	}

	r, err := RunDirect(txs, nil)
	if err != nil {
		t.Fatalf("RunDirect transfers: %v", err)
	}
	if r.TxCount != 10 {
		t.Errorf("expected 10 txs, got %d", r.TxCount)
	}
	if r.AvgGasPerTx != 21000 {
		t.Errorf("expected 21000 gas/tx, got %d", r.AvgGasPerTx)
	}
	if r.SigDuration == 0 {
		t.Error("SigDuration should be non-zero")
	}
	if r.EVMDuration == 0 {
		t.Error("EVMDuration should be non-zero")
	}
	if r.Duration != r.SigDuration+r.EVMDuration {
		t.Errorf("Duration (%v) != SigDuration (%v) + EVMDuration (%v)",
			r.Duration, r.SigDuration, r.EVMDuration)
	}
	if r.MgasPerS <= 0 {
		t.Errorf("MgasPerS should be positive, got %f", r.MgasPerS)
	}
	if r.MgasTotal <= 0 {
		t.Errorf("MgasTotal should be positive, got %f", r.MgasTotal)
	}
	if r.MgasPerS < r.MgasTotal {
		t.Errorf("EVM-only Mgas/s (%f) should be >= total Mgas/s (%f)", r.MgasPerS, r.MgasTotal)
	}
	t.Logf("Transfers: %d txs, sig=%v evm=%v total=%v, %.2f Mgas/s (EVM), %.2f Mgas/s (total)",
		r.TxCount, r.SigDuration, r.EVMDuration, r.Duration, r.MgasPerS, r.MgasTotal)
}

func TestGenerateERC20Transfers(t *testing.T) {
	txs, err := GenerateERC20Transfers(10)
	if err != nil {
		t.Fatalf("GenerateERC20Transfers: %v", err)
	}
	// 1 deploy + 10 calls
	if len(txs) != 11 {
		t.Fatalf("expected 11 txs, got %d", len(txs))
	}

	r, err := RunDirect(txs, nil)
	if err != nil {
		t.Fatalf("RunDirect ERC20: %v", err)
	}
	if r.TxCount != 11 {
		t.Errorf("expected 11 txs, got %d", r.TxCount)
	}
	if r.TotalGas < 21000*11 {
		t.Errorf("gas too low: %d", r.TotalGas)
	}
	t.Logf("ERC20: %d txs, sig=%v evm=%v, %d avg gas/tx, %.2f Mgas/s (EVM)",
		r.TxCount, r.SigDuration, r.EVMDuration, r.AvgGasPerTx, r.MgasPerS)
}

func TestGenerateStorageWrites(t *testing.T) {
	txs, err := GenerateStorageWrites(10)
	if err != nil {
		t.Fatalf("GenerateStorageWrites: %v", err)
	}
	if len(txs) != 11 {
		t.Fatalf("expected 11 txs, got %d", len(txs))
	}

	r, err := RunDirect(txs, nil)
	if err != nil {
		t.Fatalf("RunDirect storage: %v", err)
	}
	if r.TxCount != 11 {
		t.Errorf("expected 11 txs, got %d", r.TxCount)
	}
	t.Logf("Storage: %d txs, sig=%v evm=%v, %d avg gas/tx, %.2f Mgas/s (EVM)",
		r.TxCount, r.SigDuration, r.EVMDuration, r.AvgGasPerTx, r.MgasPerS)
}

func TestGenerateUniswapSwaps(t *testing.T) {
	txs, err := GenerateUniswapSwaps(10)
	if err != nil {
		t.Fatalf("GenerateUniswapSwaps: %v", err)
	}
	if len(txs) != 11 {
		t.Fatalf("expected 11 txs, got %d", len(txs))
	}

	r, err := RunDirect(txs, nil)
	if err != nil {
		t.Fatalf("RunDirect swaps: %v", err)
	}
	if r.TxCount != 11 {
		t.Errorf("expected 11 txs, got %d", r.TxCount)
	}
	t.Logf("Swaps: %d txs, sig=%v evm=%v, %d avg gas/tx, %.2f Mgas/s (EVM)",
		r.TxCount, r.SigDuration, r.EVMDuration, r.AvgGasPerTx, r.MgasPerS)
}

func TestGenerateMixedDeFi(t *testing.T) {
	txs, err := GenerateMixedDeFi(100)
	if err != nil {
		t.Fatalf("GenerateMixedDeFi: %v", err)
	}
	// 3 deploy txs + 100 workload txs
	if len(txs) != 103 {
		t.Fatalf("expected 103 txs, got %d", len(txs))
	}

	r, err := RunDirect(txs, nil)
	if err != nil {
		t.Fatalf("RunDirect mixed: %v", err)
	}
	if r.TxCount != 103 {
		t.Errorf("expected 103 txs, got %d", r.TxCount)
	}
	t.Logf("Mixed: %d txs, sig=%v evm=%v, %d avg gas/tx, %.2f Mgas/s (EVM)",
		r.TxCount, r.SigDuration, r.EVMDuration, r.AvgGasPerTx, r.MgasPerS)
}

func TestRunDirectMulti(t *testing.T) {
	gen := func() ([]*types.Transaction, error) {
		return GenerateTransfers(100)
	}
	results, err := RunDirectMulti(gen, 3, nil)
	if err != nil {
		t.Fatalf("RunDirectMulti: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	ds := ComputeStats(results)
	t.Logf("3 runs of 100 transfers:")
	t.Logf("  Sig:   %.1f +/- %.1f ms", ds.MeanSigMs, ds.StdSigMs)
	t.Logf("  EVM:   %.1f +/- %.1f ms", ds.MeanEVMMs, ds.StdEVMMs)
	t.Logf("  Total: %.1f +/- %.1f ms", ds.MeanTotalMs, ds.StdTotalMs)
	t.Logf("  Mgas/s (EVM):   %.1f +/- %.1f", ds.MeanMgasEVM, ds.StdMgasEVM)
	t.Logf("  Mgas/s (Total): %.1f +/- %.1f", ds.MeanMgasTotal, ds.StdMgasTotal)

	// EVM-only Mgas/s should be higher than total Mgas/s.
	if ds.MeanMgasEVM < ds.MeanMgasTotal {
		t.Errorf("EVM Mgas/s (%.1f) should be >= total Mgas/s (%.1f)", ds.MeanMgasEVM, ds.MeanMgasTotal)
	}
}

func TestRunDirectEmpty(t *testing.T) {
	_, err := RunDirect(nil, nil)
	if err == nil {
		t.Fatal("expected error for empty txs")
	}
}

func TestComputeStatsSingleRun(t *testing.T) {
	txs, err := GenerateTransfers(10)
	if err != nil {
		t.Fatalf("GenerateTransfers: %v", err)
	}
	r, err := RunDirect(txs, nil)
	if err != nil {
		t.Fatalf("RunDirect: %v", err)
	}

	ds := ComputeStats([]*DirectResult{r})
	if ds.StdSigMs != 0 {
		t.Errorf("stddev should be 0 for single run, got %f", ds.StdSigMs)
	}
	if ds.MeanEVMMs <= 0 {
		t.Errorf("MeanEVMMs should be positive, got %f", ds.MeanEVMMs)
	}
}

func TestParallelCompare(t *testing.T) {
	gen := func() ([]*types.Transaction, error) {
		return GenerateTransfers(50)
	}

	r, err := RunParallelCompare(gen, nil)
	if err != nil {
		t.Fatalf("RunParallelCompare: %v", err)
	}

	if r.SeqSigDuration == 0 {
		t.Error("SeqSigDuration should be non-zero")
	}
	if r.ParSigDuration == 0 {
		t.Error("ParSigDuration should be non-zero")
	}
	if r.SigSpeedup <= 0 {
		t.Errorf("SigSpeedup should be positive, got %f", r.SigSpeedup)
	}
	if r.EVMDuration == 0 {
		t.Error("EVMDuration should be non-zero")
	}

	t.Logf("Parallel comparison: seq_sig=%v par_sig=%v speedup=%.2fx evm=%v mgas/s=%.2f",
		r.SeqSigDuration, r.ParSigDuration, r.SigSpeedup, r.EVMDuration, r.MgasPerS)
}

func TestParallelCompareMulti(t *testing.T) {
	gen := func() ([]*types.Transaction, error) {
		return GenerateTransfers(50)
	}

	results, err := RunParallelCompareMulti(gen, 2, nil)
	if err != nil {
		t.Fatalf("RunParallelCompareMulti: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	ps := ComputeParallelStats(results)
	t.Logf("Parallel stats (2 runs, 50 txs):")
	t.Logf("  Seq sig: %.1f +/- %.1f ms", ps.MeanSeqSigMs, ps.StdSeqSigMs)
	t.Logf("  Par sig: %.1f +/- %.1f ms", ps.MeanParSigMs, ps.StdParSigMs)
	t.Logf("  Speedup: %.2f +/- %.2f", ps.MeanSpeedup, ps.StdSpeedup)
	t.Logf("  EVM:     %.1f +/- %.1f ms", ps.MeanEVMMs, ps.StdEVMMs)
}
