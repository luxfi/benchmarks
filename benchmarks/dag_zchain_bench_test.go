// Copyright (C) 2025-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchmarks

import (
	"testing"
)

func TestGenerateGroth16Proofs(t *testing.T) {
	proofs, err := GenerateGroth16Proofs(5, 42)
	if err != nil {
		t.Fatalf("GenerateGroth16Proofs: %v", err)
	}
	if len(proofs) != 5 {
		t.Fatalf("expected 5 proofs, got %d", len(proofs))
	}
	for i, p := range proofs {
		if len(p.PublicIn) == 0 {
			t.Errorf("proof %d has no public inputs", i)
		}
		if p.Nullifier == [32]byte{} {
			t.Errorf("proof %d has zero nullifier", i)
		}
	}
}

func TestGroth16Determinism(t *testing.T) {
	proofs1, err := GenerateGroth16Proofs(10, 99)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	proofs2, err := GenerateGroth16Proofs(10, 99)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}

	for i := range proofs1 {
		if proofs1[i].Nullifier != proofs2[i].Nullifier {
			t.Errorf("proof %d nullifier mismatch", i)
		}
		if len(proofs1[i].PublicIn) != len(proofs2[i].PublicIn) {
			t.Errorf("proof %d public input count mismatch", i)
		}
	}
}

func TestVerifyGroth16(t *testing.T) {
	proofs, err := GenerateGroth16Proofs(1, 42)
	if err != nil {
		t.Fatalf("GenerateGroth16Proofs: %v", err)
	}
	lat := verifyGroth16(proofs[0])
	if lat <= 0 {
		t.Error("verify latency should be positive")
	}
	t.Logf("Single Groth16 verify: %v", lat)
}

func TestBuildZVertices(t *testing.T) {
	proofs, err := GenerateGroth16Proofs(10, 42)
	if err != nil {
		t.Fatalf("GenerateGroth16Proofs: %v", err)
	}
	vertices := buildZVertices(proofs, 5)
	if len(vertices) == 0 {
		t.Error("expected at least one vertex")
	}

	// Verify no nullifier appears twice in any vertex.
	for _, v := range vertices {
		seen := make(map[[32]byte]struct{})
		for _, p := range v.Proofs {
			if _, ok := seen[p.Nullifier]; ok {
				t.Errorf("vertex %d has duplicate nullifier", v.Index)
			}
			seen[p.Nullifier] = struct{}{}
		}
	}

	totalProofs := 0
	for _, v := range vertices {
		totalProofs += len(v.Proofs)
	}
	if totalProofs != 10 {
		t.Errorf("expected 10 proofs across vertices, got %d", totalProofs)
	}

	t.Logf("10 proofs -> %d vertices", len(vertices))
}

func TestRunZChainBenchLinear(t *testing.T) {
	cfg := &ZChainBenchConfig{
		Seed:      42,
		NumProofs: 3,
		Runs:      1,
		Workers:   2,
	}
	result, err := RunZChainBench(ZModeLinear, cfg)
	if err != nil {
		t.Fatalf("RunZChainBench linear: %v", err)
	}
	if result.ProofCount != 3 {
		t.Errorf("expected 3 proofs, got %d", result.ProofCount)
	}
	if result.ProofsPerSec <= 0 {
		t.Errorf("proofs/sec should be positive, got %.1f", result.ProofsPerSec)
	}
	t.Logf("Z-Linear: %d proofs, %.1f proofs/sec, avg=%.2fms, p99=%.2fms",
		result.ProofCount, result.ProofsPerSec, result.AvgVerifyMs, result.P99VerifyMs)
}

func TestRunZChainBenchDAG(t *testing.T) {
	cfg := &ZChainBenchConfig{
		Seed:      42,
		NumProofs: 6,
		Runs:      1,
		Workers:   4,
	}
	result, err := RunZChainBench(ZModeDAG, cfg)
	if err != nil {
		t.Fatalf("RunZChainBench dag: %v", err)
	}
	if result.ProofCount != 6 {
		t.Errorf("expected 6 proofs, got %d", result.ProofCount)
	}
	if result.VertexCount <= 0 {
		t.Errorf("should have vertices, got %d", result.VertexCount)
	}
	t.Logf("Z-DAG: %d proofs, %d vertices, antichain=%d, %.1f proofs/sec",
		result.ProofCount, result.VertexCount, result.AntichainWidth, result.ProofsPerSec)
}

func TestRunZChainMatrix(t *testing.T) {
	cfg := &ZChainBenchConfig{
		Seed:      42,
		NumProofs: 3,
		Runs:      1,
		Workers:   2,
	}
	report, err := RunZChainMatrix(cfg)
	if err != nil {
		t.Fatalf("RunZChainMatrix: %v", err)
	}
	if report.Seed != 42 {
		t.Errorf("seed mismatch: got %d", report.Seed)
	}
	if len(report.Results) == 0 {
		t.Error("expected results in report")
	}

	md := FormatZChainMarkdownTable(report)
	if len(md) == 0 {
		t.Error("markdown should not be empty")
	}
	t.Logf("Z-Chain markdown (%d bytes):\n%s", len(md), md)

	jsonStr, err := FormatZChainJSON(report)
	if err != nil {
		t.Fatalf("FormatZChainJSON: %v", err)
	}
	if len(jsonStr) == 0 {
		t.Error("JSON should not be empty")
	}
	t.Logf("Z-Chain JSON: %d bytes", len(jsonStr))
}
