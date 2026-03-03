package bench

import (
	"fmt"
	"math"
	"math/big"
	"runtime"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

// DirectResult holds results from a direct EVM benchmark run.
type DirectResult struct {
	Workload    string
	TxCount     int
	TotalGas    uint64
	AvgGasPerTx uint64
	SigDuration time.Duration // Time to recover all signatures
	EVMDuration time.Duration // Time to execute all txs (senders cached)
	Duration    time.Duration // SigDuration + EVMDuration
	MgasPerS    float64       // Based on EVM-only time
	MgasTotal   float64       // Based on total time (sig + evm)
}

// DirectBenchConfig configures a direct EVM benchmark.
type DirectBenchConfig struct {
	ChainConfig *params.ChainConfig
}

// DefaultDirectConfig returns a config with all protocol changes enabled.
func DefaultDirectConfig() *DirectBenchConfig {
	return &DirectBenchConfig{
		ChainConfig: params.MergedTestChainConfig,
	}
}

// newStateDB creates a fresh in-memory StateDB.
func newStateDB() (*state.StateDB, error) {
	memdb := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(memdb, triedb.HashDefaults)
	sdb := state.NewDatabase(tdb, nil)
	stateDB, err := state.New(types.EmptyRootHash, sdb)
	if err != nil {
		return nil, fmt.Errorf("create statedb: %w", err)
	}
	return stateDB, nil
}

// prefundAccount sets a large ETH balance on the given address.
func prefundAccount(stateDB *state.StateDB, addr common.Address) {
	// 1 billion ETH in wei
	amount, _ := uint256.FromBig(new(big.Int).Mul(big.NewInt(1e9), big.NewInt(1e18)))
	stateDB.AddBalance(addr, amount, tracing.BalanceChangeUnspecified)
}

// recoverSenders pre-populates the signature cache on every transaction by calling
// types.Sender. Subsequent calls during ApplyTransaction hit the cache (0 ecrecover cost).
// Returns the unique sender addresses found.
func recoverSenders(txs []*types.Transaction, signer types.Signer) ([]common.Address, time.Duration, error) {
	start := time.Now()
	seen := make(map[common.Address]struct{})
	var addrs []common.Address
	for i, tx := range txs {
		addr, err := types.Sender(signer, tx)
		if err != nil {
			return nil, 0, fmt.Errorf("recover sender tx %d: %w", i, err)
		}
		if _, ok := seen[addr]; !ok {
			seen[addr] = struct{}{}
			addrs = append(addrs, addr)
		}
	}
	return addrs, time.Since(start), nil
}

// recoverSendersParallel pre-populates the signature cache using goroutines,
// similar to go-ethereum's core.SenderCacher. Returns unique senders and elapsed time.
func recoverSendersParallel(txs []*types.Transaction, signer types.Signer) ([]common.Address, time.Duration, error) {
	start := time.Now()

	workers := runtime.GOMAXPROCS(0)
	if workers > len(txs) {
		workers = len(txs)
	}

	errs := make([]error, len(txs))
	addrsAll := make([]common.Address, len(txs))

	var wg sync.WaitGroup
	ch := make(chan int, len(txs))

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				addr, err := types.Sender(signer, txs[i])
				if err != nil {
					errs[i] = err
					return
				}
				addrsAll[i] = addr
			}
		}()
	}

	for i := range txs {
		ch <- i
	}
	close(ch)
	wg.Wait()

	// Check for errors.
	for i, err := range errs {
		if err != nil {
			return nil, 0, fmt.Errorf("recover sender tx %d: %w", i, err)
		}
	}

	// Deduplicate.
	seen := make(map[common.Address]struct{})
	var unique []common.Address
	for _, addr := range addrsAll {
		if _, ok := seen[addr]; !ok {
			seen[addr] = struct{}{}
			unique = append(unique, addr)
		}
	}

	return unique, time.Since(start), nil
}

// RunDirect executes transactions directly against the EVM with zero consensus overhead.
// It measures signature recovery and EVM execution separately:
//   - SigDuration: time to recover all senders (populates tx.from cache)
//   - EVMDuration: time to execute all txs with cached senders (pure EVM)
//   - Duration: SigDuration + EVMDuration
func RunDirect(txs []*types.Transaction, cfg *DirectBenchConfig) (*DirectResult, error) {
	if cfg == nil {
		cfg = DefaultDirectConfig()
	}
	if len(txs) == 0 {
		return nil, fmt.Errorf("no transactions to execute")
	}

	signer := types.LatestSignerForChainID(cfg.ChainConfig.ChainID)

	// Phase 1: Pre-recover all senders (populates sigCache on each tx).
	senders, sigDuration, err := recoverSenders(txs, signer)
	if err != nil {
		return nil, err
	}

	// Phase 2: Set up state outside the timing loop.
	stateDB, err := newStateDB()
	if err != nil {
		return nil, err
	}

	for _, addr := range senders {
		prefundAccount(stateDB, addr)
	}

	// Commit genesis state so the trie root is valid for EVM.
	genesisRoot, err := stateDB.Commit(0, false, false)
	if err != nil {
		return nil, fmt.Errorf("commit genesis: %w", err)
	}

	// Re-open state from committed root.
	db := stateDB.Database()
	stateDB, err = state.New(genesisRoot, db)
	if err != nil {
		return nil, fmt.Errorf("reopen state: %w", err)
	}

	// Block context.
	coinbase := common.Address{}
	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash: func(n uint64) common.Hash {
			return common.Hash{}
		},
		Coinbase:    coinbase,
		GasLimit:    math.MaxUint64,
		BlockNumber: big.NewInt(1),
		Time:        uint64(time.Now().Unix()),
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		BlobBaseFee: big.NewInt(0),
	}

	vmConfig := vm.Config{NoBaseFee: true}
	evm := vm.NewEVM(blockCtx, stateDB, cfg.ChainConfig, vmConfig)

	header := &types.Header{
		Number:     big.NewInt(1),
		GasLimit:   math.MaxUint64,
		Time:       blockCtx.Time,
		BaseFee:    big.NewInt(0),
		Difficulty: big.NewInt(0),
		Coinbase:   coinbase,
	}

	gasPool := new(core.GasPool).AddGas(math.MaxUint64)

	// Phase 3: Force GC before measurement to minimize GC during EVM execution.
	runtime.GC()

	// Phase 4: Measure EVM execution only (senders are cached, no ecrecover).
	var usedGas uint64
	evmStart := time.Now()

	for i, tx := range txs {
		receipt, err := core.ApplyTransaction(evm, gasPool, stateDB, header, tx, &usedGas)
		if err != nil {
			return nil, fmt.Errorf("tx %d: %w", i, err)
		}
		if receipt.Status != types.ReceiptStatusSuccessful {
			return nil, fmt.Errorf("tx %d reverted (gas used: %d)", i, receipt.GasUsed)
		}
	}

	evmDuration := time.Since(evmStart)
	totalDuration := sigDuration + evmDuration

	mgasEVM := float64(usedGas) / evmDuration.Seconds() / 1e6
	mgasTotal := float64(usedGas) / totalDuration.Seconds() / 1e6
	avgGas := usedGas / uint64(len(txs))

	return &DirectResult{
		TxCount:     len(txs),
		TotalGas:    usedGas,
		AvgGasPerTx: avgGas,
		SigDuration: sigDuration,
		EVMDuration: evmDuration,
		Duration:    totalDuration,
		MgasPerS:    mgasEVM,
		MgasTotal:   mgasTotal,
	}, nil
}

// RunDirectMulti runs the same workload multiple times and returns all results.
func RunDirectMulti(gen func() ([]*types.Transaction, error), runs int, cfg *DirectBenchConfig) ([]*DirectResult, error) {
	results := make([]*DirectResult, 0, runs)
	for i := 0; i < runs; i++ {
		txs, err := gen()
		if err != nil {
			return nil, fmt.Errorf("generate run %d: %w", i, err)
		}
		r, err := RunDirect(txs, cfg)
		if err != nil {
			return nil, fmt.Errorf("run %d: %w", i, err)
		}
		results = append(results, r)
	}
	return results, nil
}

// DirectStats holds aggregate statistics from multiple benchmark runs.
type DirectStats struct {
	MeanSigMs  float64
	StdSigMs   float64
	MeanEVMMs  float64
	StdEVMMs   float64
	MeanTotalMs float64
	StdTotalMs  float64
	MeanMgasEVM   float64
	StdMgasEVM    float64
	MeanMgasTotal float64
	StdMgasTotal  float64
}

// Stats computes mean and standard deviation from multiple runs.
// Returns legacy-compatible values (total ms, evm mgas/s) plus full DirectStats.
func Stats(results []*DirectResult) (meanMs, stddevMs, meanMgas, stddevMgas float64) {
	ds := ComputeStats(results)
	return ds.MeanTotalMs, ds.StdTotalMs, ds.MeanMgasEVM, ds.StdMgasEVM
}

// ComputeStats computes full statistics across all timing dimensions.
func ComputeStats(results []*DirectResult) DirectStats {
	n := float64(len(results))
	if n == 0 {
		return DirectStats{}
	}

	var sumSig, sumEVM, sumTotal, sumMgasEVM, sumMgasTotal float64
	for _, r := range results {
		sumSig += float64(r.SigDuration.Microseconds()) / 1000.0
		sumEVM += float64(r.EVMDuration.Microseconds()) / 1000.0
		sumTotal += float64(r.Duration.Microseconds()) / 1000.0
		sumMgasEVM += r.MgasPerS
		sumMgasTotal += r.MgasTotal
	}

	ds := DirectStats{
		MeanSigMs:     sumSig / n,
		MeanEVMMs:     sumEVM / n,
		MeanTotalMs:   sumTotal / n,
		MeanMgasEVM:   sumMgasEVM / n,
		MeanMgasTotal: sumMgasTotal / n,
	}

	if n < 2 {
		return ds
	}

	var varSig, varEVM, varTotal, varMgasEVM, varMgasTotal float64
	for _, r := range results {
		sig := float64(r.SigDuration.Microseconds()) / 1000.0
		evm := float64(r.EVMDuration.Microseconds()) / 1000.0
		total := float64(r.Duration.Microseconds()) / 1000.0

		varSig += (sig - ds.MeanSigMs) * (sig - ds.MeanSigMs)
		varEVM += (evm - ds.MeanEVMMs) * (evm - ds.MeanEVMMs)
		varTotal += (total - ds.MeanTotalMs) * (total - ds.MeanTotalMs)
		varMgasEVM += (r.MgasPerS - ds.MeanMgasEVM) * (r.MgasPerS - ds.MeanMgasEVM)
		varMgasTotal += (r.MgasTotal - ds.MeanMgasTotal) * (r.MgasTotal - ds.MeanMgasTotal)
	}

	ds.StdSigMs = math.Sqrt(varSig / (n - 1))
	ds.StdEVMMs = math.Sqrt(varEVM / (n - 1))
	ds.StdTotalMs = math.Sqrt(varTotal / (n - 1))
	ds.StdMgasEVM = math.Sqrt(varMgasEVM / (n - 1))
	ds.StdMgasTotal = math.Sqrt(varMgasTotal / (n - 1))

	return ds
}
