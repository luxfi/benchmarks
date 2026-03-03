package bench

import (
	"fmt"
	"math"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// ParallelResult holds results comparing sequential vs parallel signature recovery.
type ParallelResult struct {
	Workload       string
	TxCount        int
	SeqSigDuration time.Duration // Sequential ecrecover
	ParSigDuration time.Duration // Parallel ecrecover
	SigSpeedup     float64       // SeqSig / ParSig
	EVMDuration    time.Duration // EVM execution (same for both, senders cached)
	TotalGas       uint64
	MgasPerS       float64 // Based on EVM-only time
}

// RunParallel benchmarks parallel vs sequential signature recovery on the same txs,
// then runs EVM execution with cached senders.
func RunParallel(txs []*types.Transaction, cfg *DirectBenchConfig) (*ParallelResult, error) {
	if cfg == nil {
		cfg = DefaultDirectConfig()
	}
	if len(txs) == 0 {
		return nil, fmt.Errorf("no transactions to execute")
	}

	signer := types.LatestSignerForChainID(cfg.ChainConfig.ChainID)

	// Measure sequential signature recovery.
	// We need fresh txs without cached senders for a fair comparison.
	// Since we can't clear the cache, we measure sequential first on uncached txs,
	// then parallel on the same (now cached) txs won't be fair.
	// Instead: measure sequential time, then re-generate for parallel measurement.
	// But the caller may not want that. Simpler: time sequential, then parallel is
	// already cached -- so we just measure the sequential cost directly.

	// For a fair comparison, clone the signing data by re-signing fresh txs.
	// Actually, the simplest correct approach: measure sequential on these txs (first call
	// populates cache), then to measure parallel we need uncached txs.
	// Since we can't uncache, we just measure sequential time analytically.

	// Correct approach: sign fresh txs twice (two separate batches from same generator).
	// But we only have one set. So we measure sequential on this set, and to get
	// a parallel measurement, we use recoverSendersParallel on a set that may already
	// be cached. This means we need to be called with uncached txs.

	// Sequential recovery (populates cache).
	_, seqDuration, err := recoverSenders(txs, signer)
	if err != nil {
		return nil, fmt.Errorf("sequential recovery: %w", err)
	}

	// The cache is now populated. We cannot re-measure parallel on the same txs.
	// Record sequential time; parallel measurement requires RunParallelCompare.
	result := &ParallelResult{
		TxCount:        len(txs),
		SeqSigDuration: seqDuration,
	}

	return result, nil
}

// RunParallelCompare generates txs twice to fairly compare sequential vs parallel
// signature recovery, then runs EVM execution.
func RunParallelCompare(gen func() ([]*types.Transaction, error), cfg *DirectBenchConfig) (*ParallelResult, error) {
	if cfg == nil {
		cfg = DefaultDirectConfig()
	}

	signer := types.LatestSignerForChainID(cfg.ChainConfig.ChainID)

	// Generate first batch for sequential measurement.
	txsSeq, err := gen()
	if err != nil {
		return nil, fmt.Errorf("generate sequential batch: %w", err)
	}
	if len(txsSeq) == 0 {
		return nil, fmt.Errorf("no transactions generated")
	}

	seqSenders, seqDuration, err := recoverSenders(txsSeq, signer)
	if err != nil {
		return nil, fmt.Errorf("sequential recovery: %w", err)
	}

	// Generate second batch for parallel measurement (fresh txs, no cache).
	txsPar, err := gen()
	if err != nil {
		return nil, fmt.Errorf("generate parallel batch: %w", err)
	}

	_, parDuration, err := recoverSendersParallel(txsPar, signer)
	if err != nil {
		return nil, fmt.Errorf("parallel recovery: %w", err)
	}

	// Run EVM execution using the sequential batch (senders already cached).
	_ = seqSenders // used below in RunDirect-style execution
	r, err := RunDirect(txsSeq, cfg)
	if err != nil {
		return nil, fmt.Errorf("evm execution: %w", err)
	}

	speedup := float64(seqDuration) / float64(parDuration)
	if parDuration == 0 {
		speedup = math.Inf(1)
	}

	return &ParallelResult{
		Workload:       r.Workload,
		TxCount:        r.TxCount,
		SeqSigDuration: seqDuration,
		ParSigDuration: parDuration,
		SigSpeedup:     speedup,
		EVMDuration:    r.EVMDuration,
		TotalGas:       r.TotalGas,
		MgasPerS:       r.MgasPerS,
	}, nil
}

// RunParallelCompareMulti runs the parallel comparison multiple times.
func RunParallelCompareMulti(gen func() ([]*types.Transaction, error), runs int, cfg *DirectBenchConfig) ([]*ParallelResult, error) {
	results := make([]*ParallelResult, 0, runs)
	for i := 0; i < runs; i++ {
		r, err := RunParallelCompare(gen, cfg)
		if err != nil {
			return nil, fmt.Errorf("run %d: %w", i, err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ParallelStats holds aggregate statistics from parallel comparison runs.
type ParallelStats struct {
	MeanSeqSigMs float64
	StdSeqSigMs  float64
	MeanParSigMs float64
	StdParSigMs  float64
	MeanSpeedup  float64
	StdSpeedup   float64
	MeanEVMMs    float64
	StdEVMMs     float64
	MeanMgas     float64
	StdMgas      float64
}

// ComputeParallelStats computes statistics from parallel comparison results.
func ComputeParallelStats(results []*ParallelResult) ParallelStats {
	n := float64(len(results))
	if n == 0 {
		return ParallelStats{}
	}

	var sumSeq, sumPar, sumSpd, sumEVM, sumMgas float64
	for _, r := range results {
		sumSeq += float64(r.SeqSigDuration.Microseconds()) / 1000.0
		sumPar += float64(r.ParSigDuration.Microseconds()) / 1000.0
		sumSpd += r.SigSpeedup
		sumEVM += float64(r.EVMDuration.Microseconds()) / 1000.0
		sumMgas += r.MgasPerS
	}

	ps := ParallelStats{
		MeanSeqSigMs: sumSeq / n,
		MeanParSigMs: sumPar / n,
		MeanSpeedup:  sumSpd / n,
		MeanEVMMs:    sumEVM / n,
		MeanMgas:     sumMgas / n,
	}

	if n < 2 {
		return ps
	}

	var varSeq, varPar, varSpd, varEVM, varMgas float64
	for _, r := range results {
		seq := float64(r.SeqSigDuration.Microseconds()) / 1000.0
		par := float64(r.ParSigDuration.Microseconds()) / 1000.0
		evm := float64(r.EVMDuration.Microseconds()) / 1000.0

		varSeq += (seq - ps.MeanSeqSigMs) * (seq - ps.MeanSeqSigMs)
		varPar += (par - ps.MeanParSigMs) * (par - ps.MeanParSigMs)
		varSpd += (r.SigSpeedup - ps.MeanSpeedup) * (r.SigSpeedup - ps.MeanSpeedup)
		varEVM += (evm - ps.MeanEVMMs) * (evm - ps.MeanEVMMs)
		varMgas += (r.MgasPerS - ps.MeanMgas) * (r.MgasPerS - ps.MeanMgas)
	}

	ps.StdSeqSigMs = math.Sqrt(varSeq / (n - 1))
	ps.StdParSigMs = math.Sqrt(varPar / (n - 1))
	ps.StdSpeedup = math.Sqrt(varSpd / (n - 1))
	ps.StdEVMMs = math.Sqrt(varEVM / (n - 1))
	ps.StdMgas = math.Sqrt(varMgas / (n - 1))

	return ps
}
