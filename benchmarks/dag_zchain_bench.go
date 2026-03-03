// Copyright (C) 2025-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package benchmarks contains Lux blockchain benchmarks.
package benchmarks

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/consensys/gnark-crypto/ecc/bn254"
)

// Z-Chain DAG Execution Modes
const (
	ZModeLinear = "z-linear" // Sequential: 1 proof per block, serial verify
	ZModeDAG    = "z-dag"    // DAG Z: N proofs per vertex, parallel Groth16 verify
)

// ZChainBenchConfig configures the Z-Chain DAG benchmark.
type ZChainBenchConfig struct {
	Seed       int64 // Deterministic PRNG seed
	NumProofs  int   // Number of ZK proofs to verify
	Runs       int   // Repeat for statistics
	Workers    int   // Parallel verifiers (0 = GOMAXPROCS)
	VertexSize int   // Max proofs per vertex (0 = auto)
}

// DefaultZChainBenchConfig returns sensible defaults.
func DefaultZChainBenchConfig() *ZChainBenchConfig {
	return &ZChainBenchConfig{
		Seed:       42,
		NumProofs:  100,
		Runs:       3,
		Workers:    0,
		VertexSize: 0,
	}
}

// SimulatedGroth16Proof represents a synthetic Groth16 proof.
// Verification performs real bn254 pairing checks to faithfully
// represent the computational cost of Groth16 verification.
type SimulatedGroth16Proof struct {
	Index int

	// Proof elements: A in G1, B in G2, C in G1
	A bn254.G1Affine
	B bn254.G2Affine
	C bn254.G1Affine

	// Public inputs as G1 points (IC terms)
	PublicIn []bn254.G1Affine

	// Verification key elements
	VKAlpha bn254.G1Affine
	VKBeta  bn254.G2Affine
	VKGamma bn254.G2Affine
	VKDelta bn254.G2Affine

	// Unique nullifier for DAG ordering
	Nullifier [32]byte
}

// ZVertex represents a group of proofs that can be verified in parallel.
// Proofs within one vertex have non-overlapping nullifier sets.
type ZVertex struct {
	Index  int
	Proofs []SimulatedGroth16Proof
}

// ZChainResult holds metrics from a single Z-Chain benchmark run.
type ZChainResult struct {
	Mode            string        `json:"mode"`
	ProofCount      int           `json:"proof_count"`
	VertexCount     int           `json:"vertex_count"`
	AntichainWidth  int           `json:"antichain_width"` // Max proofs in any single vertex
	Duration        time.Duration `json:"duration_ns"`
	ProofsPerSec    float64       `json:"proofs_per_sec"`
	AvgVerifyMs     float64       `json:"avg_verify_ms"`
	P99VerifyMs     float64       `json:"p99_verify_ms"`
	SpeedupVsLinear float64       `json:"speedup_vs_linear"`
}

// ZChainReport holds the full Z-Chain benchmark output.
type ZChainReport struct {
	Seed      int64                     `json:"seed"`
	GitSHA    string                    `json:"git_sha"`
	Host      ZHostInfo                 `json:"host"`
	Timestamp string                    `json:"timestamp"`
	Results   map[string][]ZChainResult `json:"results"` // mode -> []result
}

// ZHostInfo identifies the benchmark host.
type ZHostInfo struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	CPUs   int    `json:"cpus"`
	GOMAXP int    `json:"gomaxprocs"`
}

func getZHostInfo() ZHostInfo {
	return ZHostInfo{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		CPUs:   runtime.NumCPU(),
		GOMAXP: runtime.GOMAXPROCS(0),
	}
}

func getZGitSHA() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// randomG1 generates a random G1 point via scalar mult of the generator.
func randomG1() (bn254.G1Affine, error) {
	s, err := rand.Int(rand.Reader, bn254.ID.ScalarField())
	if err != nil {
		return bn254.G1Affine{}, err
	}
	_, _, g1gen, _ := bn254.Generators()
	var pt bn254.G1Affine
	pt.ScalarMultiplication(&g1gen, s)
	return pt, nil
}

// randomG2 generates a random G2 point via scalar mult of the generator.
func randomG2() (bn254.G2Affine, error) {
	s, err := rand.Int(rand.Reader, bn254.ID.ScalarField())
	if err != nil {
		return bn254.G2Affine{}, err
	}
	_, _, _, g2gen := bn254.Generators()
	var pt bn254.G2Affine
	pt.ScalarMultiplication(&g2gen, s)
	return pt, nil
}

// GenerateGroth16Proofs creates N synthetic but computationally representative
// Groth16 proofs. Each proof requires real bn254 pairing-based verification.
//
// The nullifiers are deterministic for the given seed so that DAG construction
// is reproducible.
func GenerateGroth16Proofs(n int, seed int64) ([]SimulatedGroth16Proof, error) {
	rng := mrand.New(mrand.NewSource(seed))
	proofs := make([]SimulatedGroth16Proof, n)

	// Shared verification key (one circuit, many proofs).
	vkAlpha, err := randomG1()
	if err != nil {
		return nil, fmt.Errorf("gen vk alpha: %w", err)
	}
	vkBeta, err := randomG2()
	if err != nil {
		return nil, fmt.Errorf("gen vk beta: %w", err)
	}
	vkGamma, err := randomG2()
	if err != nil {
		return nil, fmt.Errorf("gen vk gamma: %w", err)
	}
	vkDelta, err := randomG2()
	if err != nil {
		return nil, fmt.Errorf("gen vk delta: %w", err)
	}

	for i := 0; i < n; i++ {
		a, err := randomG1()
		if err != nil {
			return nil, fmt.Errorf("gen proof %d A: %w", i, err)
		}
		b, err := randomG2()
		if err != nil {
			return nil, fmt.Errorf("gen proof %d B: %w", i, err)
		}
		c, err := randomG1()
		if err != nil {
			return nil, fmt.Errorf("gen proof %d C: %w", i, err)
		}

		// 1-3 public inputs
		numInputs := 1 + rng.Intn(3)
		publicIn := make([]bn254.G1Affine, numInputs)
		for j := 0; j < numInputs; j++ {
			pt, err := randomG1()
			if err != nil {
				return nil, fmt.Errorf("gen proof %d input %d: %w", i, j, err)
			}
			publicIn[j] = pt
		}

		// Deterministic nullifier from seed.
		var nullifier [32]byte
		nval := rng.Int63()
		for b := 0; b < 8; b++ {
			nullifier[b] = byte(nval >> (b * 8))
		}
		nullifier[8] = byte(i & 0xff)
		nullifier[9] = byte(i >> 8)

		proofs[i] = SimulatedGroth16Proof{
			Index:     i,
			A:         a,
			B:         b,
			C:         c,
			PublicIn:  publicIn,
			VKAlpha:   vkAlpha,
			VKBeta:    vkBeta,
			VKGamma:   vkGamma,
			VKDelta:   vkDelta,
			Nullifier: nullifier,
		}
	}
	return proofs, nil
}

// verifyGroth16 performs a computationally faithful Groth16 verification.
//
// Real Groth16: e(A, B) == e(alpha, beta) * e(sum(pubInput_i * vk_i), gamma) * e(C, delta)
// We perform the pairing checks to measure the real computational cost.
// The proof is synthetic (won't satisfy the relation), but the cost of
// executing the pairings is identical.
func verifyGroth16(proof SimulatedGroth16Proof) time.Duration {
	start := time.Now()

	// Accumulate public inputs via G1 scalar mult (simulates IC accumulation).
	var accum bn254.G1Affine
	if len(proof.PublicIn) > 0 {
		accum = proof.PublicIn[0]
		for i := 1; i < len(proof.PublicIn); i++ {
			accum.Add(&accum, &proof.PublicIn[i])
		}
	}

	// Groth16 verification pairing check:
	// e(-A, B) * e(alpha, beta) * e(accum, gamma) * e(C, delta) == 1
	//
	// This is the standard 4-pairing check. We negate A for the check.
	var negA bn254.G1Affine
	negA.Neg(&proof.A)

	g1s := []bn254.G1Affine{negA, proof.VKAlpha, accum, proof.C}
	g2s := []bn254.G2Affine{proof.B, proof.VKBeta, proof.VKGamma, proof.VKDelta}

	// Execute the multi-pairing (this is the expensive part).
	_, _ = bn254.PairingCheck(g1s, g2s)

	return time.Since(start)
}

// buildZVertices partitions proofs into non-conflicting vertices.
// Two proofs conflict if they share a nullifier.
func buildZVertices(proofs []SimulatedGroth16Proof, maxPerVertex int) []ZVertex {
	type tracker struct {
		vertex     ZVertex
		nullifiers map[[32]byte]struct{}
	}

	var vertices []tracker

	for _, p := range proofs {
		placed := false
		for vi := range vertices {
			if len(vertices[vi].vertex.Proofs) >= maxPerVertex {
				continue
			}
			if _, conflict := vertices[vi].nullifiers[p.Nullifier]; conflict {
				continue
			}
			vertices[vi].vertex.Proofs = append(vertices[vi].vertex.Proofs, p)
			vertices[vi].nullifiers[p.Nullifier] = struct{}{}
			placed = true
			break
		}
		if !placed {
			vt := tracker{
				vertex:     ZVertex{Index: len(vertices), Proofs: []SimulatedGroth16Proof{p}},
				nullifiers: map[[32]byte]struct{}{p.Nullifier: {}},
			}
			vertices = append(vertices, vt)
		}
	}

	result := make([]ZVertex, len(vertices))
	for i := range vertices {
		result[i] = vertices[i].vertex
	}
	return result
}

// RunZChainBench runs a single Z-Chain benchmark.
func RunZChainBench(mode string, cfg *ZChainBenchConfig) (*ZChainResult, error) {
	if cfg == nil {
		cfg = DefaultZChainBenchConfig()
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	vertexSize := cfg.VertexSize
	if vertexSize <= 0 {
		vertexSize = int(math.Sqrt(float64(cfg.NumProofs)))
		if vertexSize < 1 {
			vertexSize = 1
		}
	}

	proofs, err := GenerateGroth16Proofs(cfg.NumProofs, cfg.Seed)
	if err != nil {
		return nil, fmt.Errorf("generate proofs: %w", err)
	}

	vertices := buildZVertices(proofs, vertexSize)

	antichainWidth := 0
	for _, v := range vertices {
		if len(v.Proofs) > antichainWidth {
			antichainWidth = len(v.Proofs)
		}
	}

	switch mode {
	case ZModeLinear:
		return runZLinear(proofs, vertices, antichainWidth)
	case ZModeDAG:
		return runZDAG(proofs, vertices, antichainWidth, workers)
	default:
		return nil, fmt.Errorf("unknown z-chain mode: %s", mode)
	}
}

// runZLinear: verify all proofs sequentially, one at a time.
func runZLinear(proofs []SimulatedGroth16Proof, vertices []ZVertex, antichainWidth int) (*ZChainResult, error) {
	var latencies []time.Duration

	runtime.GC()
	start := time.Now()

	for _, p := range proofs {
		lat := verifyGroth16(p)
		latencies = append(latencies, lat)
	}

	total := time.Since(start)
	return buildZResult(ZModeLinear, proofs, vertices, antichainWidth, total, latencies, 0), nil
}

// runZDAG: partition proofs into vertices, verify within each vertex in parallel.
func runZDAG(proofs []SimulatedGroth16Proof, vertices []ZVertex, antichainWidth, workers int) (*ZChainResult, error) {
	var allLatencies []time.Duration
	var latMu sync.Mutex

	runtime.GC()
	start := time.Now()

	for _, vertex := range vertices {
		if len(vertex.Proofs) == 1 {
			lat := verifyGroth16(vertex.Proofs[0])
			latMu.Lock()
			allLatencies = append(allLatencies, lat)
			latMu.Unlock()
			continue
		}

		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup

		for _, p := range vertex.Proofs {
			wg.Add(1)
			sem <- struct{}{}
			go func(proof SimulatedGroth16Proof) {
				defer wg.Done()
				defer func() { <-sem }()
				lat := verifyGroth16(proof)
				latMu.Lock()
				allLatencies = append(allLatencies, lat)
				latMu.Unlock()
			}(p)
		}
		wg.Wait()
	}

	total := time.Since(start)
	return buildZResult(ZModeDAG, proofs, vertices, antichainWidth, total, allLatencies, 0), nil
}

func buildZResult(mode string, proofs []SimulatedGroth16Proof, vertices []ZVertex, antichainWidth int, total time.Duration, latencies []time.Duration, speedup float64) *ZChainResult {
	proofsPerSec := float64(len(proofs)) / total.Seconds()

	var avgMs float64
	if len(latencies) > 0 {
		var sum float64
		for _, l := range latencies {
			sum += float64(l.Nanoseconds()) / 1e6
		}
		avgMs = sum / float64(len(latencies))
	}

	p99Ms := computeZP99(latencies)

	return &ZChainResult{
		Mode:            mode,
		ProofCount:      len(proofs),
		VertexCount:     len(vertices),
		AntichainWidth:  antichainWidth,
		Duration:        total,
		ProofsPerSec:    proofsPerSec,
		AvgVerifyMs:     avgMs,
		P99VerifyMs:     p99Ms,
		SpeedupVsLinear: speedup,
	}
}

func computeZP99(latencies []time.Duration) float64 {
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

// RunZChainMatrix runs all Z-Chain benchmark modes and returns a report.
func RunZChainMatrix(cfg *ZChainBenchConfig) (*ZChainReport, error) {
	if cfg == nil {
		cfg = DefaultZChainBenchConfig()
	}

	modes := []string{ZModeLinear, ZModeDAG}
	report := &ZChainReport{
		Seed:      cfg.Seed,
		GitSHA:    getZGitSHA(),
		Host:      getZHostInfo(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Results:   make(map[string][]ZChainResult),
	}

	var linearDuration time.Duration

	for _, mode := range modes {
		var allResults []ZChainResult

		for run := 0; run < cfg.Runs; run++ {
			runCfg := *cfg
			runCfg.Seed = cfg.Seed + int64(run)

			result, err := RunZChainBench(mode, &runCfg)
			if err != nil {
				return nil, fmt.Errorf("%s run %d: %w", mode, run, err)
			}

			if mode == ZModeLinear && run == 0 {
				linearDuration = result.Duration
			}

			if mode != ZModeLinear && linearDuration > 0 && result.Duration > 0 {
				result.SpeedupVsLinear = float64(linearDuration) / float64(result.Duration)
			}

			allResults = append(allResults, *result)
		}

		report.Results[mode] = allResults
	}

	return report, nil
}

// FormatZChainMarkdownTable formats Z-Chain results as markdown.
func FormatZChainMarkdownTable(report *ZChainReport) string {
	var sb strings.Builder

	sb.WriteString("## DAG Z-Chain Benchmark Results\n\n")
	sb.WriteString(fmt.Sprintf("**Seed**: %d | **Git**: %s | **Host**: %s/%s %d CPUs\n\n",
		report.Seed, report.GitSHA, report.Host.OS, report.Host.Arch, report.Host.CPUs))

	sb.WriteString("| Mode | Proofs | Vertices | Antichain | Proofs/s | Avg Verify (ms) | P99 Verify (ms) | Speedup |\n")
	sb.WriteString("|------|--------|----------|-----------|----------|-----------------|-----------------|--------|\n")

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
		avg := averageZResults(results)
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %.1f | %.2f | %.2f | %.2fx |\n",
			avg.Mode, avg.ProofCount, avg.VertexCount, avg.AntichainWidth,
			avg.ProofsPerSec, avg.AvgVerifyMs, avg.P99VerifyMs, avg.SpeedupVsLinear))
	}

	return sb.String()
}

// FormatZChainJSON returns the report as indented JSON.
func FormatZChainJSON(report *ZChainReport) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func averageZResults(results []ZChainResult) ZChainResult {
	if len(results) == 0 {
		return ZChainResult{}
	}
	avg := results[0]
	if len(results) == 1 {
		return avg
	}

	n := float64(len(results))
	var sumPPS, sumAvg, sumP99, sumSpeedup float64
	for _, r := range results {
		sumPPS += r.ProofsPerSec
		sumAvg += r.AvgVerifyMs
		sumP99 += r.P99VerifyMs
		sumSpeedup += r.SpeedupVsLinear
	}
	avg.ProofsPerSec = sumPPS / n
	avg.AvgVerifyMs = sumAvg / n
	avg.P99VerifyMs = sumP99 / n
	avg.SpeedupVsLinear = sumSpeedup / n
	return avg
}

// Use a blank import guard so the compiler doesn't complain about unused big.
var _ = new(big.Int)
