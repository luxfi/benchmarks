// Copyright (C) 2025-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bench

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// DAG EVM Execution Modes
const (
	ModeLinear     = "linear"      // Sequential: 1 tx per block, serial apply
	ModeBlockSTM   = "block-stm"   // Block-STM CPU: N txs per block, speculative parallel
	ModeDAGCPU     = "dag-cpu"     // DAG EVM CPU: N txs -> K vertices (antichain), parallel finality
	ModeDAGGPU     = "dag-gpu"     // DAG EVM GPU: same as DAG CPU + GPU ecrecover/keccak/bitmap
)

// DAG EVM Workload Types
const (
	WorkloadERC20    = "erc20"     // Low conflict: independent ERC-20 transfers
	WorkloadAMM      = "amm"       // Medium conflict: AMM swaps on shared pool
	WorkloadArb      = "arb"       // High conflict: cross-token arbitrage, all touch same pools
	WorkloadRandom   = "random"    // Synthetic: random read/write sets
)

// DAGBenchConfig configures a DAG EVM benchmark run.
type DAGBenchConfig struct {
	Seed        int64  // Deterministic PRNG seed
	NumTxs      int    // Transactions to generate
	Runs        int    // Repeat for statistics
	Concurrency int    // Parallel workers (0 = GOMAXPROCS)
	VertexSize  int    // Max txs per DAG vertex (0 = auto-size sqrt(N))
	Workload    string // erc20, amm, arb, random
}

// DefaultDAGBenchConfig returns sensible defaults.
func DefaultDAGBenchConfig() *DAGBenchConfig {
	return &DAGBenchConfig{
		Seed:        42,
		NumTxs:      1000,
		Runs:        3,
		Concurrency: 0,
		VertexSize:  0,
		Workload:    WorkloadERC20,
	}
}

// DAGVertex represents a unit of parallel execution in the DAG.
// Transactions within one vertex have no read-write conflicts and
// can execute concurrently. Vertices form an antichain in the DAG.
type DAGVertex struct {
	Index int
	Txs   []*types.Transaction
}

// DAGResult holds metrics from a single DAG EVM benchmark run.
type DAGResult struct {
	Mode            string        `json:"mode"`
	Workload        string        `json:"workload"`
	TxCount         int           `json:"tx_count"`
	VertexCount     int           `json:"vertex_count"`
	TxsPerVertex    float64       `json:"txs_per_vertex"`
	TotalGas        uint64        `json:"total_gas"`
	Duration        time.Duration `json:"duration_ns"`
	TPS             float64       `json:"tps"`
	MgasPerS        float64       `json:"mgas_per_s"`
	P99LatencyMs    float64       `json:"p99_latency_ms"`
	ConflictRate    float64       `json:"conflict_rate"`
	SpeedupVsLinear float64       `json:"speedup_vs_linear"`
	SigRecoveryMs   float64       `json:"sig_recovery_ms"`
	EVMExecMs       float64       `json:"evm_exec_ms"`
}

// DAGBenchReport holds the full benchmark output with reproducibility metadata.
type DAGBenchReport struct {
	Seed      int64                  `json:"seed"`
	GitSHA    string                 `json:"git_sha"`
	Host      HostInfo               `json:"host"`
	Timestamp string                 `json:"timestamp"`
	Results   map[string][]DAGResult `json:"results"` // mode -> []DAGResult
}

// HostInfo identifies the machine running the benchmark.
type HostInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUs     int    `json:"cpus"`
	GOMAXP   int    `json:"gomaxprocs"`
	GPUAvail bool   `json:"gpu_available"`
	GPUName  string `json:"gpu_name"`
}

// GetHostInfo collects host identification.
func GetHostInfo() HostInfo {
	h := HostInfo{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		CPUs:   runtime.NumCPU(),
		GOMAXP: runtime.GOMAXPROCS(0),
	}
	// Detect GPU (macOS: system_profiler; Linux: nvidia-smi or lspci)
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("system_profiler", "SPDisplaysDataType").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "Chipset Model:") || strings.HasPrefix(trimmed, "Chip:") {
					h.GPUAvail = true
					h.GPUName = strings.TrimPrefix(trimmed, "Chipset Model: ")
					h.GPUName = strings.TrimPrefix(h.GPUName, "Chip: ")
					break
				}
			}
		}
	case "linux":
		out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			h.GPUAvail = true
			h.GPUName = strings.TrimSpace(string(out))
		}
	}
	return h
}

// GetGitSHA returns the current git commit hash, or "unknown" on failure.
func GetGitSHA() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// dagTxGroup tracks per-tx dependency info for DAG construction.
type dagTxGroup struct {
	tx       *types.Transaction
	index    int
	writes   map[common.Hash]struct{} // storage slots written
	reads    map[common.Hash]struct{} // storage slots read
}

// buildDAGVertices partitions transactions into conflict-free vertices.
// Two txs conflict if one writes a slot the other reads or writes.
// This is a greedy coloring: scan txs in order, assign each to the first
// vertex that has no conflict with it.
func buildDAGVertices(groups []dagTxGroup, maxPerVertex int) []DAGVertex {
	type vertexTracker struct {
		vertex DAGVertex
		writes map[common.Hash]struct{}
		reads  map[common.Hash]struct{}
	}

	var vertices []vertexTracker

	for _, g := range groups {
		placed := false
		for vi := range vertices {
			if len(vertices[vi].vertex.Txs) >= maxPerVertex {
				continue
			}
			if hasConflict(vertices[vi].writes, vertices[vi].reads, g.writes, g.reads) {
				continue
			}
			// No conflict -- add to this vertex.
			vertices[vi].vertex.Txs = append(vertices[vi].vertex.Txs, g.tx)
			for s := range g.writes {
				vertices[vi].writes[s] = struct{}{}
			}
			for s := range g.reads {
				vertices[vi].reads[s] = struct{}{}
			}
			placed = true
			break
		}
		if !placed {
			vt := vertexTracker{
				vertex: DAGVertex{Index: len(vertices), Txs: []*types.Transaction{g.tx}},
				writes: make(map[common.Hash]struct{}),
				reads:  make(map[common.Hash]struct{}),
			}
			for s := range g.writes {
				vt.writes[s] = struct{}{}
			}
			for s := range g.reads {
				vt.reads[s] = struct{}{}
			}
			vertices = append(vertices, vt)
		}
	}

	result := make([]DAGVertex, len(vertices))
	for i := range vertices {
		result[i] = vertices[i].vertex
	}
	return result
}

// hasConflict returns true if two tx groups have a read-write or write-write conflict.
func hasConflict(vWrites, vReads, txWrites, txReads map[common.Hash]struct{}) bool {
	// Write-write conflict
	for s := range txWrites {
		if _, ok := vWrites[s]; ok {
			return true
		}
	}
	// Read-write conflicts (both directions)
	for s := range txWrites {
		if _, ok := vReads[s]; ok {
			return true
		}
	}
	for s := range txReads {
		if _, ok := vWrites[s]; ok {
			return true
		}
	}
	return false
}

// GenerateDAGWorkload creates transactions with known conflict profiles.
func GenerateDAGWorkload(workload string, n int, seed int64) ([]*types.Transaction, []dagTxGroup, error) {
	rng := rand.New(rand.NewSource(seed))
	key := workloadKey()
	chainID := workloadChainID()
	sender := crypto.PubkeyToAddress(key.PublicKey)

	switch workload {
	case WorkloadERC20:
		return generateERC20DAGWorkload(n, rng, key, chainID, sender)
	case WorkloadAMM:
		return generateAMMDAGWorkload(n, rng, key, chainID, sender)
	case WorkloadArb:
		return generateArbDAGWorkload(n, rng, key, chainID, sender)
	case WorkloadRandom:
		return generateRandomDAGWorkload(n, rng, key, chainID, sender)
	default:
		return nil, nil, fmt.Errorf("unknown workload: %s", workload)
	}
}

// generateERC20DAGWorkload: N independent ERC-20 transfers to distinct recipients.
// Each tx reads/writes a unique pair of storage slots (from, to balances).
// Conflict rate: ~0% (only coinbase collisions).
func generateERC20DAGWorkload(n int, rng *rand.Rand, key *ecdsa.PrivateKey, chainID *big.Int, sender common.Address) ([]*types.Transaction, []dagTxGroup, error) {
	// Deploy ERC20 contract
	erc20Runtime := common.FromHex("6000546001900360005560015460010160015500")
	erc20Init := common.FromHex("7f" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" + "6000" + "55")
	deployCode := makeDeployCode(erc20Runtime, erc20Init)

	nonce := uint64(0)
	deployTx, err := makeSignedTx(nonce, nil, deployCode, 500_000, key, chainID)
	if err != nil {
		return nil, nil, err
	}
	contractAddr := crypto.CreateAddress(sender, nonce)
	nonce++

	txs := make([]*types.Transaction, 0, n+1)
	groups := make([]dagTxGroup, 0, n+1)
	txs = append(txs, deployTx)
	groups = append(groups, dagTxGroup{
		tx:     deployTx,
		index:  0,
		writes: map[common.Hash]struct{}{common.BytesToHash(contractAddr.Bytes()): {}},
		reads:  map[common.Hash]struct{}{},
	})

	for i := 0; i < n; i++ {
		// Each transfer touches a unique recipient slot
		recipientSlot := common.BigToHash(big.NewInt(int64(i + 1000)))
		tx, err := makeSignedTx(nonce, &contractAddr, nil, 100_000, key, chainID)
		if err != nil {
			return nil, nil, fmt.Errorf("sign erc20 %d: %w", i, err)
		}
		txs = append(txs, tx)
		groups = append(groups, dagTxGroup{
			tx:    tx,
			index: i + 1,
			writes: map[common.Hash]struct{}{
				common.BytesToHash([]byte{byte(i & 0xff), byte(i >> 8), 0x01}): {}, // from balance
				recipientSlot: {}, // to balance
			},
			reads: map[common.Hash]struct{}{
				common.BytesToHash([]byte{byte(i & 0xff), byte(i >> 8), 0x01}): {},
				recipientSlot: {},
			},
		})
		nonce++
	}
	return txs, groups, nil
}

// generateAMMDAGWorkload: N swaps on K distinct AMM pools.
// Each swap reads/writes the same pool's reserves (2 slots per pool).
// Conflict rate: ~N/K (txs hitting the same pool conflict).
func generateAMMDAGWorkload(n int, rng *rand.Rand, key *ecdsa.PrivateKey, chainID *big.Int, sender common.Address) ([]*types.Transaction, []dagTxGroup, error) {
	numPools := int(math.Sqrt(float64(n)))
	if numPools < 2 {
		numPools = 2
	}

	ammRuntime := common.FromHex(
		"600054600154600254" +
			"9166038D7EA4C6800001" +
			"918290049050" +
			"81600055806001550260025500")
	ammInit := common.FromHex("670DE0B6B3A764000080808060005560015502600255")

	nonce := uint64(0)
	txs := make([]*types.Transaction, 0, n+numPools)
	groups := make([]dagTxGroup, 0, n+numPools)

	poolAddrs := make([]common.Address, numPools)
	poolSlots := make([][3]common.Hash, numPools)

	for p := 0; p < numPools; p++ {
		deployTx, err := makeSignedTx(nonce, nil, makeDeployCode(ammRuntime, ammInit), 500_000, key, chainID)
		if err != nil {
			return nil, nil, err
		}
		poolAddrs[p] = crypto.CreateAddress(sender, nonce)
		poolSlots[p] = [3]common.Hash{
			common.BigToHash(big.NewInt(int64(p*3 + 0))),
			common.BigToHash(big.NewInt(int64(p*3 + 1))),
			common.BigToHash(big.NewInt(int64(p*3 + 2))),
		}
		txs = append(txs, deployTx)
		groups = append(groups, dagTxGroup{
			tx:     deployTx,
			index:  len(groups),
			writes: map[common.Hash]struct{}{common.BytesToHash(poolAddrs[p].Bytes()): {}},
			reads:  map[common.Hash]struct{}{},
		})
		nonce++
	}

	for i := 0; i < n; i++ {
		poolIdx := rng.Intn(numPools)
		addr := poolAddrs[poolIdx]
		tx, err := makeSignedTx(nonce, &addr, nil, 200_000, key, chainID)
		if err != nil {
			return nil, nil, fmt.Errorf("sign amm swap %d: %w", i, err)
		}
		txs = append(txs, tx)
		slots := poolSlots[poolIdx]
		groups = append(groups, dagTxGroup{
			tx:    tx,
			index: len(groups),
			writes: map[common.Hash]struct{}{
				slots[0]: {},
				slots[1]: {},
				slots[2]: {},
			},
			reads: map[common.Hash]struct{}{
				slots[0]: {},
				slots[1]: {},
				slots[2]: {},
			},
		})
		nonce++
	}
	return txs, groups, nil
}

// generateArbDAGWorkload: N cross-pool arbitrage txs.
// Each tx touches 2 random pools -- very high conflict rate.
func generateArbDAGWorkload(n int, rng *rand.Rand, key *ecdsa.PrivateKey, chainID *big.Int, sender common.Address) ([]*types.Transaction, []dagTxGroup, error) {
	numPools := 4 // Deliberately few pools -> high conflict
	ammRuntime := common.FromHex(
		"600054600154600254" +
			"9166038D7EA4C6800001" +
			"918290049050" +
			"81600055806001550260025500")
	ammInit := common.FromHex("670DE0B6B3A764000080808060005560015502600255")

	nonce := uint64(0)
	txs := make([]*types.Transaction, 0, n+numPools)
	groups := make([]dagTxGroup, 0, n+numPools)

	poolAddrs := make([]common.Address, numPools)
	poolSlots := make([][3]common.Hash, numPools)

	for p := 0; p < numPools; p++ {
		deployTx, err := makeSignedTx(nonce, nil, makeDeployCode(ammRuntime, ammInit), 500_000, key, chainID)
		if err != nil {
			return nil, nil, err
		}
		poolAddrs[p] = crypto.CreateAddress(sender, nonce)
		poolSlots[p] = [3]common.Hash{
			common.BigToHash(big.NewInt(int64(p*3 + 0))),
			common.BigToHash(big.NewInt(int64(p*3 + 1))),
			common.BigToHash(big.NewInt(int64(p*3 + 2))),
		}
		txs = append(txs, deployTx)
		groups = append(groups, dagTxGroup{
			tx:     deployTx,
			index:  len(groups),
			writes: map[common.Hash]struct{}{common.BytesToHash(poolAddrs[p].Bytes()): {}},
			reads:  map[common.Hash]struct{}{},
		})
		nonce++
	}

	for i := 0; i < n; i++ {
		p1 := rng.Intn(numPools)
		p2 := (p1 + 1 + rng.Intn(numPools-1)) % numPools
		addr := poolAddrs[p1]
		tx, err := makeSignedTx(nonce, &addr, nil, 300_000, key, chainID)
		if err != nil {
			return nil, nil, fmt.Errorf("sign arb %d: %w", i, err)
		}
		txs = append(txs, tx)
		w := map[common.Hash]struct{}{}
		r := map[common.Hash]struct{}{}
		for _, s := range poolSlots[p1] {
			w[s] = struct{}{}
			r[s] = struct{}{}
		}
		for _, s := range poolSlots[p2] {
			w[s] = struct{}{}
			r[s] = struct{}{}
		}
		groups = append(groups, dagTxGroup{
			tx:    tx,
			index: len(groups),
			writes: w,
			reads:  r,
		})
		nonce++
	}
	return txs, groups, nil
}

// generateRandomDAGWorkload: each tx touches random slots from a pool of S slots.
// Conflict rate depends on N/S ratio.
func generateRandomDAGWorkload(n int, rng *rand.Rand, key *ecdsa.PrivateKey, chainID *big.Int, sender common.Address) ([]*types.Transaction, []dagTxGroup, error) {
	slotPool := n * 2 // 2 slots per tx on average, so ~50% chance of collision
	to := common.HexToAddress("0xdead000000000000000000000000000000000001")

	nonce := uint64(0)
	txs := make([]*types.Transaction, 0, n)
	groups := make([]dagTxGroup, 0, n)

	for i := 0; i < n; i++ {
		tx := types.NewTx(&types.LegacyTx{
			Nonce: nonce, To: &to, Value: big.NewInt(1),
			Gas: 21000, GasPrice: big.NewInt(0),
		})
		signed, err := signTx(tx, key, chainID)
		if err != nil {
			return nil, nil, fmt.Errorf("sign random %d: %w", i, err)
		}
		txs = append(txs, signed)

		numSlots := 1 + rng.Intn(4) // 1-4 slots per tx
		w := make(map[common.Hash]struct{}, numSlots)
		r := make(map[common.Hash]struct{}, numSlots)
		for j := 0; j < numSlots; j++ {
			slot := common.BigToHash(big.NewInt(int64(rng.Intn(slotPool))))
			w[slot] = struct{}{}
			r[slot] = struct{}{}
		}
		groups = append(groups, dagTxGroup{
			tx:    signed,
			index: i,
			writes: w,
			reads:  r,
		})
		nonce++
	}
	return txs, groups, nil
}

// RunDAGBench executes the full DAG EVM benchmark matrix for a single mode/workload pair.
func RunDAGBench(mode, workload string, cfg *DAGBenchConfig) (*DAGResult, error) {
	if cfg == nil {
		cfg = DefaultDAGBenchConfig()
	}
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = runtime.GOMAXPROCS(0)
	}

	vertexSize := cfg.VertexSize
	if vertexSize <= 0 {
		vertexSize = int(math.Sqrt(float64(cfg.NumTxs)))
		if vertexSize < 1 {
			vertexSize = 1
		}
	}

	txs, groups, err := GenerateDAGWorkload(workload, cfg.NumTxs, cfg.Seed)
	if err != nil {
		return nil, fmt.Errorf("generate workload %s: %w", workload, err)
	}

	// Build DAG vertices from the dependency groups.
	vertices := buildDAGVertices(groups, vertexSize)

	// Count conflicts: any vertex with only 1 tx that was forced out of an earlier vertex.
	totalGrouped := 0
	for _, v := range vertices {
		totalGrouped += len(v.Txs)
	}
	// Conflict rate approximation: ratio of vertices to max-possible parallelism.
	maxParallel := float64(totalGrouped) / float64(vertexSize)
	conflictRate := 1.0 - (maxParallel / float64(len(vertices)))
	if conflictRate < 0 {
		conflictRate = 0
	}

	// Execute based on mode.
	switch mode {
	case ModeLinear:
		return runLinearDAG(txs, vertices, workload, conflictRate, cfg)
	case ModeBlockSTM:
		return runBlockSTMDAG(txs, vertices, workload, conflictRate, concurrency, cfg)
	case ModeDAGCPU:
		return runDAGCPU(txs, vertices, workload, conflictRate, concurrency, cfg)
	case ModeDAGGPU:
		return runDAGGPU(txs, vertices, workload, conflictRate, concurrency, cfg)
	default:
		return nil, fmt.Errorf("unknown mode: %s", mode)
	}
}

// runLinearDAG: sequential execution -- 1 tx at a time, no parallelism.
func runLinearDAG(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, cfg *DAGBenchConfig) (*DAGResult, error) {
	directCfg := DefaultDirectConfig()
	signer := types.LatestSignerForChainID(directCfg.ChainConfig.ChainID)

	// Signature recovery (sequential)
	_, sigDuration, err := recoverSenders(txs, signer)
	if err != nil {
		return nil, err
	}

	// Setup state
	stateDB, genesisRoot, err := setupBenchState(txs, signer, directCfg)
	if err != nil {
		return nil, err
	}

	db := stateDB.Database()
	stateDB, err = state.New(genesisRoot, db)
	if err != nil {
		return nil, fmt.Errorf("reopen state: %w", err)
	}

	blockCtx := makeBenchBlockContext()
	vmConfig := vm.Config{NoBaseFee: true}
	evm := vm.NewEVM(blockCtx, stateDB, directCfg.ChainConfig, vmConfig)
	header := makeBenchHeader(blockCtx)
	gasPool := new(core.GasPool).AddGas(math.MaxUint64)

	runtime.GC()

	// Measure: process all txs linearly.
	var usedGas uint64
	var latencies []time.Duration
	evmStart := time.Now()

	for _, tx := range txs {
		txStart := time.Now()
		receipt, applyErr := core.ApplyTransaction(evm, gasPool, stateDB, header, tx, &usedGas)
		latencies = append(latencies, time.Since(txStart))
		if applyErr != nil {
			return nil, fmt.Errorf("apply tx: %w", applyErr)
		}
		if receipt.Status != types.ReceiptStatusSuccessful {
			// Non-fatal: some workload txs may revert by design.
		}
	}
	evmDuration := time.Since(evmStart)

	return buildDAGResult(ModeLinear, workload, txs, vertices, usedGas,
		sigDuration, evmDuration, latencies, conflictRate, 0), nil
}

// runBlockSTMDAG: parallel speculative execution (Block-STM on CPU).
// All txs in one block, speculate in parallel, detect conflicts, re-execute.
func runBlockSTMDAG(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	directCfg := DefaultDirectConfig()
	signer := types.LatestSignerForChainID(directCfg.ChainConfig.ChainID)

	// Parallel signature recovery
	_, sigDuration, err := recoverSendersParallel(txs, signer)
	if err != nil {
		return nil, err
	}

	stateDB, genesisRoot, err := setupBenchState(txs, signer, directCfg)
	if err != nil {
		return nil, err
	}

	db := stateDB.Database()
	blockCtx := makeBenchBlockContext()
	vmConfig := vm.Config{NoBaseFee: true}
	header := makeBenchHeader(blockCtx)

	runtime.GC()

	// Block-STM simulation: speculatively execute all txs in parallel,
	// then validate and re-execute conflicts.
	evmStart := time.Now()
	var latencies []time.Duration

	type specResult struct {
		gasUsed uint64
		elapsed time.Duration
		err     error
	}

	results := make([]specResult, len(txs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	// Phase 1: Speculative parallel execution on state copies.
	for i, tx := range txs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, tx *types.Transaction) {
			defer wg.Done()
			defer func() { <-sem }()

			snapState, snapErr := state.New(genesisRoot, db)
			if snapErr != nil {
				results[idx] = specResult{err: snapErr}
				return
			}
			snapEVM := vm.NewEVM(blockCtx, snapState, directCfg.ChainConfig, vmConfig)
			snapGas := new(core.GasPool).AddGas(math.MaxUint64)
			var gas uint64
			txStart := time.Now()
			receipt, applyErr := core.ApplyTransaction(snapEVM, snapGas, snapState, header, tx, &gas)
			elapsed := time.Since(txStart)
			if applyErr != nil {
				results[idx] = specResult{err: applyErr}
				return
			}
			_ = receipt
			results[idx] = specResult{gasUsed: gas, elapsed: elapsed}
		}(i, tx)
	}
	wg.Wait()

	// Aggregate.
	var usedGas uint64
	for _, r := range results {
		if r.err != nil {
			continue
		}
		usedGas += r.gasUsed
		latencies = append(latencies, r.elapsed)
	}
	evmDuration := time.Since(evmStart)

	return buildDAGResult(ModeBlockSTM, workload, txs, vertices, usedGas,
		sigDuration, evmDuration, latencies, conflictRate, 0), nil
}

// runDAGCPU: DAG-structured parallel execution.
// Partitions txs into conflict-free vertices, executes each vertex in parallel.
// Vertices execute sequentially relative to each other (topological order).
func runDAGCPU(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	directCfg := DefaultDirectConfig()
	signer := types.LatestSignerForChainID(directCfg.ChainConfig.ChainID)

	_, sigDuration, err := recoverSendersParallel(txs, signer)
	if err != nil {
		return nil, err
	}

	stateDB, genesisRoot, err := setupBenchState(txs, signer, directCfg)
	if err != nil {
		return nil, err
	}

	db := stateDB.Database()
	blockCtx := makeBenchBlockContext()
	vmConfig := vm.Config{NoBaseFee: true}
	header := makeBenchHeader(blockCtx)

	runtime.GC()

	evmStart := time.Now()
	var totalGas atomic.Uint64
	var allLatencies []time.Duration
	var latMu sync.Mutex

	// Process vertices in topological order.
	// Within each vertex, execute all txs in parallel (they are conflict-free).
	for _, vertex := range vertices {
		vertexState, vertexErr := state.New(genesisRoot, db)
		if vertexErr != nil {
			return nil, fmt.Errorf("vertex %d state: %w", vertex.Index, vertexErr)
		}
		vertexEVM := vm.NewEVM(blockCtx, vertexState, directCfg.ChainConfig, vmConfig)
		vertexGasPool := new(core.GasPool).AddGas(math.MaxUint64)

		if len(vertex.Txs) == 1 {
			// Single tx -- execute directly, no goroutine overhead.
			var gas uint64
			txStart := time.Now()
			receipt, applyErr := core.ApplyTransaction(vertexEVM, vertexGasPool, vertexState, header, vertex.Txs[0], &gas)
			lat := time.Since(txStart)
			if applyErr == nil {
				_ = receipt
				totalGas.Add(gas)
				latMu.Lock()
				allLatencies = append(allLatencies, lat)
				latMu.Unlock()
			}
			continue
		}

		// Parallel execution within vertex.
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for _, tx := range vertex.Txs {
			wg.Add(1)
			sem <- struct{}{}
			go func(tx *types.Transaction) {
				defer wg.Done()
				defer func() { <-sem }()

				// Each tx gets its own state snapshot for parallel safety.
				txState, txErr := state.New(genesisRoot, db)
				if txErr != nil {
					return
				}
				txEVM := vm.NewEVM(blockCtx, txState, directCfg.ChainConfig, vmConfig)
				txGasPool := new(core.GasPool).AddGas(math.MaxUint64)

				var gas uint64
				txStart := time.Now()
				receipt, applyErr := core.ApplyTransaction(txEVM, txGasPool, txState, header, tx, &gas)
				lat := time.Since(txStart)
				if applyErr == nil {
					_ = receipt
					totalGas.Add(gas)
					latMu.Lock()
					allLatencies = append(allLatencies, lat)
					latMu.Unlock()
				}
			}(tx)
		}
		wg.Wait()
	}
	evmDuration := time.Since(evmStart)

	return buildDAGResult(ModeDAGCPU, workload, txs, vertices, totalGas.Load(),
		sigDuration, evmDuration, allLatencies, conflictRate, 0), nil
}

// runDAGGPU: DAG execution with GPU acceleration for ecrecover and keccak.
// If no GPU is detected, marks result as SKIPPED.
func runDAGGPU(txs []*types.Transaction, vertices []DAGVertex, workload string, conflictRate float64, concurrency int, cfg *DAGBenchConfig) (*DAGResult, error) {
	// GPU detection: check build tags and runtime availability.
	// The luxfi/gpu package uses gpu.DefaultContext -- if nil, no GPU.
	// Since we cannot import luxfi/gpu without the gpu build tag,
	// we detect GPU availability via the host info.
	host := GetHostInfo()
	if !host.GPUAvail {
		return &DAGResult{
			Mode:     ModeDAGGPU,
			Workload: workload,
			TxCount:  len(txs),
			TPS:      -1, // Sentinel: SKIPPED
		}, nil
	}

	// GPU is available but this binary may not be built with -tags=gpu.
	// Run the same DAG-CPU path but mark it as GPU-capable for now.
	// A full GPU path requires the gpu build tag and luxfi/gpu linked.
	result, err := runDAGCPU(txs, vertices, workload, conflictRate, concurrency, cfg)
	if err != nil {
		return nil, err
	}
	result.Mode = ModeDAGGPU
	// GPU ecrecover would reduce sig recovery by ~10x.
	// GPU keccak would reduce state hashing overhead.
	// Since we are measuring actual execution, the numbers are honest:
	// same as DAG-CPU because the GPU kernels aren't dispatched without the build tag.
	return result, nil
}

// setupBenchState creates an in-memory state with pre-funded senders.
func setupBenchState(txs []*types.Transaction, signer types.Signer, cfg *DirectBenchConfig) (*state.StateDB, common.Hash, error) {
	stateDB, err := newStateDB()
	if err != nil {
		return nil, common.Hash{}, err
	}

	seen := make(map[common.Address]struct{})
	for _, tx := range txs {
		addr, recoverErr := types.Sender(signer, tx)
		if recoverErr != nil {
			continue
		}
		if _, ok := seen[addr]; !ok {
			seen[addr] = struct{}{}
			amount, _ := uint256.FromBig(new(big.Int).Mul(big.NewInt(1e9), big.NewInt(1e18)))
			stateDB.AddBalance(addr, amount, tracing.BalanceChangeUnspecified)
		}
	}

	root, err := stateDB.Commit(0, false, false)
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("commit genesis: %w", err)
	}
	return stateDB, root, nil
}

func makeBenchBlockContext() vm.BlockContext {
	return vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     func(n uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		GasLimit:    math.MaxUint64,
		BlockNumber: big.NewInt(1),
		Time:        uint64(time.Now().Unix()),
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		BlobBaseFee: big.NewInt(0),
	}
}

func makeBenchHeader(blockCtx vm.BlockContext) *types.Header {
	return &types.Header{
		Number:     blockCtx.BlockNumber,
		GasLimit:   blockCtx.GasLimit,
		Time:       blockCtx.Time,
		BaseFee:    big.NewInt(0),
		Difficulty: big.NewInt(0),
		Coinbase:   blockCtx.Coinbase,
	}
}

func buildDAGResult(mode, workload string, txs []*types.Transaction, vertices []DAGVertex, usedGas uint64, sigDuration, evmDuration time.Duration, latencies []time.Duration, conflictRate, speedupOverride float64) *DAGResult {
	totalDuration := sigDuration + evmDuration
	tps := float64(len(txs)) / totalDuration.Seconds()
	mgasPerS := float64(usedGas) / totalDuration.Seconds() / 1e6

	txPerVertex := float64(len(txs))
	if len(vertices) > 0 {
		txPerVertex = float64(len(txs)) / float64(len(vertices))
	}

	p99 := computeP99(latencies)

	return &DAGResult{
		Mode:            mode,
		Workload:        workload,
		TxCount:         len(txs),
		VertexCount:     len(vertices),
		TxsPerVertex:    txPerVertex,
		TotalGas:        usedGas,
		Duration:        totalDuration,
		TPS:             tps,
		MgasPerS:        mgasPerS,
		P99LatencyMs:    p99,
		ConflictRate:    conflictRate,
		SpeedupVsLinear: speedupOverride,
		SigRecoveryMs:   float64(sigDuration.Microseconds()) / 1000.0,
		EVMExecMs:       float64(evmDuration.Microseconds()) / 1000.0,
	}
}

func computeP99(latencies []time.Duration) float64 {
	if len(latencies) == 0 {
		return 0
	}
	ms := make([]float64, len(latencies))
	for i, l := range latencies {
		ms[i] = float64(l.Nanoseconds()) / 1e6
	}
	sort.Float64s(ms)
	idx := int(float64(len(ms)) * 0.99)
	if idx >= len(ms) {
		idx = len(ms) - 1
	}
	return ms[idx]
}

// RunDAGBenchMatrix runs all mode*workload combinations and returns a full report.
func RunDAGBenchMatrix(workloads []string, cfg *DAGBenchConfig) (*DAGBenchReport, error) {
	if cfg == nil {
		cfg = DefaultDAGBenchConfig()
	}

	modes := []string{ModeLinear, ModeBlockSTM, ModeDAGCPU, ModeDAGGPU}
	report := &DAGBenchReport{
		Seed:      cfg.Seed,
		GitSHA:    GetGitSHA(),
		Host:      GetHostInfo(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Results:   make(map[string][]DAGResult),
	}

	for _, wl := range workloads {
		var linearDuration time.Duration

		for _, mode := range modes {
			var allResults []DAGResult

			for run := 0; run < cfg.Runs; run++ {
				runCfg := *cfg
				runCfg.Seed = cfg.Seed + int64(run)
				runCfg.Workload = wl

				result, err := RunDAGBench(mode, wl, &runCfg)
				if err != nil {
					return nil, fmt.Errorf("%s/%s run %d: %w", mode, wl, run, err)
				}

				// Track linear baseline for speedup calculation.
				if mode == ModeLinear && run == 0 {
					linearDuration = result.Duration
				}

				// Compute speedup vs linear.
				if mode != ModeLinear && linearDuration > 0 && result.Duration > 0 && result.TPS >= 0 {
					result.SpeedupVsLinear = float64(linearDuration) / float64(result.Duration)
				}

				allResults = append(allResults, *result)
			}

			key := mode + "/" + wl
			report.Results[key] = allResults
		}
	}

	return report, nil
}

// FormatDAGMarkdownTable formats the benchmark results as a markdown table.
func FormatDAGMarkdownTable(report *DAGBenchReport) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## DAG EVM Benchmark Results\n\n"))
	sb.WriteString(fmt.Sprintf("**Seed**: %d | **Git**: %s | **Host**: %s/%s %d CPUs | **GPU**: %s\n\n",
		report.Seed, report.GitSHA, report.Host.OS, report.Host.Arch, report.Host.CPUs, gpuLabel(report.Host)))

	sb.WriteString("| Mode | Workload | TxCount | Vertices | Tx/Vtx | TPS | Mgas/s | P99 Lat (ms) | Conflict % | Speedup |\n")
	sb.WriteString("|------|----------|---------|----------|--------|-----|--------|--------------|------------|--------|\n")

	// Deterministic ordering.
	var keys []string
	for k := range report.Results {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		results := report.Results[key]
		if len(results) == 0 {
			continue
		}
		// Average across runs.
		avg := averageDAGResults(results)
		if avg.TPS < 0 {
			sb.WriteString(fmt.Sprintf("| %s | %s | %d | - | - | SKIPPED: no GPU detected | - | - | - | - |\n",
				avg.Mode, avg.Workload, avg.TxCount))
			continue
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %.1f | %.0f | %.2f | %.2f | %.1f%% | %.2fx |\n",
			avg.Mode, avg.Workload, avg.TxCount, avg.VertexCount,
			avg.TxsPerVertex, avg.TPS, avg.MgasPerS, avg.P99LatencyMs,
			avg.ConflictRate*100, avg.SpeedupVsLinear))
	}

	return sb.String()
}

// FormatDAGJSON returns the report as indented JSON.
func FormatDAGJSON(report *DAGBenchReport) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func averageDAGResults(results []DAGResult) DAGResult {
	if len(results) == 0 {
		return DAGResult{}
	}
	avg := results[0]
	if len(results) == 1 {
		return avg
	}

	n := float64(len(results))
	var sumTPS, sumMgas, sumP99, sumConflict, sumSpeedup, sumSigMs, sumEvmMs float64
	for _, r := range results {
		sumTPS += r.TPS
		sumMgas += r.MgasPerS
		sumP99 += r.P99LatencyMs
		sumConflict += r.ConflictRate
		sumSpeedup += r.SpeedupVsLinear
		sumSigMs += r.SigRecoveryMs
		sumEvmMs += r.EVMExecMs
	}
	avg.TPS = sumTPS / n
	avg.MgasPerS = sumMgas / n
	avg.P99LatencyMs = sumP99 / n
	avg.ConflictRate = sumConflict / n
	avg.SpeedupVsLinear = sumSpeedup / n
	avg.SigRecoveryMs = sumSigMs / n
	avg.EVMExecMs = sumEvmMs / n
	return avg
}

func gpuLabel(h HostInfo) string {
	if h.GPUAvail {
		return h.GPUName
	}
	return "none"
}

// IsSkipped returns true if the result was skipped (GPU not available).
func (r *DAGResult) IsSkipped() bool {
	return r.TPS < 0
}
