// Copyright (C) 2025-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bench

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestGenerateDAGWorkloadERC20(t *testing.T) {
	txs, groups, err := GenerateDAGWorkload(WorkloadERC20, 50, 42)
	if err != nil {
		t.Fatalf("GenerateDAGWorkload erc20: %v", err)
	}
	// 1 deploy + 50 calls
	if len(txs) != 51 {
		t.Fatalf("expected 51 txs, got %d", len(txs))
	}
	if len(groups) != 51 {
		t.Fatalf("expected 51 groups, got %d", len(groups))
	}
	// ERC20 transfers should have very low conflict.
	vertices := buildDAGVertices(groups, 10)
	t.Logf("ERC20: %d txs -> %d vertices (%.1f tx/vtx)", len(txs), len(vertices),
		float64(len(txs))/float64(len(vertices)))
}

func TestGenerateDAGWorkloadAMM(t *testing.T) {
	txs, groups, err := GenerateDAGWorkload(WorkloadAMM, 100, 42)
	if err != nil {
		t.Fatalf("GenerateDAGWorkload amm: %v", err)
	}
	if len(groups) != len(txs) {
		t.Fatalf("group count mismatch: %d txs, %d groups", len(txs), len(groups))
	}
	vertices := buildDAGVertices(groups, 20)
	t.Logf("AMM: %d txs -> %d vertices (%.1f tx/vtx)", len(txs), len(vertices),
		float64(len(txs))/float64(len(vertices)))
	// AMM should have more vertices than ERC20 due to pool conflicts.
	if len(vertices) < 2 {
		t.Error("expected multiple vertices for AMM workload")
	}
}

func TestGenerateDAGWorkloadArb(t *testing.T) {
	txs, groups, err := GenerateDAGWorkload(WorkloadArb, 50, 42)
	if err != nil {
		t.Fatalf("GenerateDAGWorkload arb: %v", err)
	}
	vertices := buildDAGVertices(groups, 10)
	t.Logf("Arb: %d txs -> %d vertices (%.1f tx/vtx)", len(txs), len(vertices),
		float64(len(txs))/float64(len(vertices)))
	// Arb should have the most vertices (highest conflict).
}

func TestGenerateDAGWorkloadRandom(t *testing.T) {
	txs, groups, err := GenerateDAGWorkload(WorkloadRandom, 100, 42)
	if err != nil {
		t.Fatalf("GenerateDAGWorkload random: %v", err)
	}
	if len(txs) != 100 {
		t.Fatalf("expected 100 txs, got %d", len(txs))
	}
	vertices := buildDAGVertices(groups, 20)
	t.Logf("Random: %d txs -> %d vertices (%.1f tx/vtx)", len(txs), len(vertices),
		float64(len(txs))/float64(len(vertices)))
}

func TestBuildDAGVertices(t *testing.T) {
	// Two independent txs should go in the same vertex.
	txs, err := GenerateTransfers(2)
	if err != nil {
		t.Fatalf("GenerateTransfers: %v", err)
	}

	slot1 := common.Hash{1}
	slot2 := common.Hash{2}

	groups := []dagTxGroup{
		{tx: txs[0], index: 0, writes: map[common.Hash]struct{}{slot1: {}}, reads: map[common.Hash]struct{}{}},
		{tx: txs[1], index: 1, writes: map[common.Hash]struct{}{slot2: {}}, reads: map[common.Hash]struct{}{}},
	}
	vertices := buildDAGVertices(groups, 10)
	if len(vertices) != 1 {
		t.Errorf("expected 1 vertex for non-conflicting txs, got %d", len(vertices))
	}

	// Two conflicting txs should be in different vertices.
	conflicting := []dagTxGroup{
		{tx: txs[0], index: 0, writes: map[common.Hash]struct{}{slot1: {}}, reads: map[common.Hash]struct{}{}},
		{tx: txs[1], index: 1, writes: map[common.Hash]struct{}{slot1: {}}, reads: map[common.Hash]struct{}{slot1: {}}},
	}
	vertices = buildDAGVertices(conflicting, 10)
	if len(vertices) != 2 {
		t.Errorf("expected 2 vertices for conflicting txs, got %d", len(vertices))
	}
}

func TestRunDAGBenchLinearERC20(t *testing.T) {
	cfg := &DAGBenchConfig{
		Seed:     42,
		NumTxs:   20,
		Runs:     1,
		Workload: WorkloadERC20,
	}
	result, err := RunDAGBench(ModeLinear, WorkloadERC20, cfg)
	if err != nil {
		t.Fatalf("RunDAGBench linear/erc20: %v", err)
	}
	if result.TxCount != 21 { // 1 deploy + 20
		t.Errorf("expected 21 txs, got %d", result.TxCount)
	}
	if result.TPS <= 0 {
		t.Errorf("TPS should be positive, got %.1f", result.TPS)
	}
	if result.MgasPerS <= 0 {
		t.Errorf("Mgas/s should be positive, got %.2f", result.MgasPerS)
	}
	t.Logf("Linear/ERC20: %d txs, TPS=%.0f, Mgas/s=%.2f, P99=%.2fms, sig=%.1fms, evm=%.1fms",
		result.TxCount, result.TPS, result.MgasPerS, result.P99LatencyMs,
		result.SigRecoveryMs, result.EVMExecMs)
}

func TestRunDAGBenchBlockSTM(t *testing.T) {
	cfg := &DAGBenchConfig{
		Seed:     42,
		NumTxs:   20,
		Runs:     1,
		Workload: WorkloadERC20,
	}
	result, err := RunDAGBench(ModeBlockSTM, WorkloadERC20, cfg)
	if err != nil {
		t.Fatalf("RunDAGBench block-stm/erc20: %v", err)
	}
	if result.TPS <= 0 {
		t.Errorf("TPS should be positive, got %.1f", result.TPS)
	}
	t.Logf("BlockSTM/ERC20: %d txs, TPS=%.0f, Mgas/s=%.2f, sig=%.1fms, evm=%.1fms",
		result.TxCount, result.TPS, result.MgasPerS, result.SigRecoveryMs, result.EVMExecMs)
}

func TestRunDAGBenchDAGCPU(t *testing.T) {
	cfg := &DAGBenchConfig{
		Seed:     42,
		NumTxs:   20,
		Runs:     1,
		Workload: WorkloadERC20,
	}
	result, err := RunDAGBench(ModeDAGCPU, WorkloadERC20, cfg)
	if err != nil {
		t.Fatalf("RunDAGBench dag-cpu/erc20: %v", err)
	}
	if result.TPS <= 0 {
		t.Errorf("TPS should be positive, got %.1f", result.TPS)
	}
	if result.VertexCount <= 0 {
		t.Errorf("should have vertices, got %d", result.VertexCount)
	}
	t.Logf("DAG-CPU/ERC20: %d txs, %d vertices, TPS=%.0f, Mgas/s=%.2f, conflict=%.1f%%",
		result.TxCount, result.VertexCount, result.TPS, result.MgasPerS, result.ConflictRate*100)
}

func TestRunDAGBenchDAGGPU(t *testing.T) {
	cfg := &DAGBenchConfig{
		Seed:     42,
		NumTxs:   10,
		Runs:     1,
		Workload: WorkloadERC20,
	}
	result, err := RunDAGBench(ModeDAGGPU, WorkloadERC20, cfg)
	if err != nil {
		t.Fatalf("RunDAGBench dag-gpu/erc20: %v", err)
	}
	if result.IsSkipped() {
		t.Logf("DAG-GPU/ERC20: SKIPPED (no GPU detected)")
	} else {
		t.Logf("DAG-GPU/ERC20: %d txs, TPS=%.0f", result.TxCount, result.TPS)
	}
}

func TestRunDAGBenchMatrix(t *testing.T) {
	cfg := &DAGBenchConfig{
		Seed:   42,
		NumTxs: 10,
		Runs:   1,
	}
	workloads := []string{WorkloadERC20, WorkloadRandom}
	report, err := RunDAGBenchMatrix(workloads, cfg)
	if err != nil {
		t.Fatalf("RunDAGBenchMatrix: %v", err)
	}
	if report.Seed != 42 {
		t.Errorf("seed mismatch: got %d", report.Seed)
	}
	if report.GitSHA == "" {
		t.Error("git sha should not be empty")
	}
	if len(report.Results) == 0 {
		t.Error("expected results in report")
	}

	md := FormatDAGMarkdownTable(report)
	if len(md) == 0 {
		t.Error("markdown table should not be empty")
	}
	t.Logf("Markdown output (%d bytes):\n%s", len(md), md)

	jsonStr, err := FormatDAGJSON(report)
	if err != nil {
		t.Fatalf("FormatDAGJSON: %v", err)
	}
	if len(jsonStr) == 0 {
		t.Error("JSON output should not be empty")
	}
	t.Logf("JSON output: %d bytes", len(jsonStr))
}

func TestDAGDeterminism(t *testing.T) {
	// Same seed must produce identical tx sets.
	txs1, groups1, err := GenerateDAGWorkload(WorkloadRandom, 50, 12345)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	txs2, groups2, err := GenerateDAGWorkload(WorkloadRandom, 50, 12345)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}

	if len(txs1) != len(txs2) {
		t.Fatalf("tx count mismatch: %d vs %d", len(txs1), len(txs2))
	}
	for i := range txs1 {
		if txs1[i].Hash() != txs2[i].Hash() {
			t.Errorf("tx %d hash mismatch", i)
		}
	}
	if len(groups1) != len(groups2) {
		t.Fatalf("group count mismatch: %d vs %d", len(groups1), len(groups2))
	}
}

func TestGetHostInfo(t *testing.T) {
	h := GetHostInfo()
	if h.OS == "" {
		t.Error("OS should not be empty")
	}
	if h.CPUs <= 0 {
		t.Error("CPUs should be positive")
	}
	t.Logf("Host: %s/%s, %d CPUs, GPU=%v (%s)", h.OS, h.Arch, h.CPUs, h.GPUAvail, h.GPUName)
}

func TestRunDAGBenchCEVMSeq(t *testing.T) {
	cfg := &DAGBenchConfig{Seed: 42, NumTxs: 100, Runs: 1, Workload: WorkloadERC20}
	r, err := RunDAGBench(ModeCEVMSeq, WorkloadERC20, cfg)
	if err != nil {
		t.Skipf("cevm-seq unavailable: %v", err)
	}
	t.Logf("CEVM-Seq: %d txs, TPS=%.0f, Mgas/s=%.2f, evm=%.1fms",
		r.TxCount, r.TPS, r.MgasPerS, r.EVMExecMs)
}

func TestRunDAGBenchCEVMPar(t *testing.T) {
	cfg := &DAGBenchConfig{Seed: 42, NumTxs: 100, Runs: 1, Workload: WorkloadERC20}
	r, err := RunDAGBench(ModeCEVMPar, WorkloadERC20, cfg)
	if err != nil {
		t.Skipf("cevm-par unavailable: %v", err)
	}
	t.Logf("CEVM-Par: %d txs, TPS=%.0f, Mgas/s=%.2f, evm=%.1fms",
		r.TxCount, r.TPS, r.MgasPerS, r.EVMExecMs)
}

func TestRunDAGBenchCEVMGPU(t *testing.T) {
	cfg := &DAGBenchConfig{Seed: 42, NumTxs: 100, Runs: 1, Workload: WorkloadERC20}
	r, err := RunDAGBench(ModeCEVMGPU, WorkloadERC20, cfg)
	if err != nil {
		t.Skipf("cevm-gpu unavailable: %v", err)
	}
	t.Logf("CEVM-GPU: %d txs, TPS=%.0f, Mgas/s=%.2f, evm=%.1fms",
		r.TxCount, r.TPS, r.MgasPerS, r.EVMExecMs)
}
