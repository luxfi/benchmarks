// Copyright (C) 2019-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/luxfi/cevm"
)

// CellResult is one (workload, n, backend) measurement after all repeats.
type CellResult struct {
	Workload string  `json:"workload"`
	Backend  string  `json:"backend"`
	N        int     `json:"n"`
	Runs     int     `json:"runs"`
	Skipped  bool    `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`

	// Timing in milliseconds (median across runs).
	WallMsMedian float64 `json:"wall_ms_median"`
	WallMsStdDev float64 `json:"wall_ms_stddev"`
	ExecMsMedian float64 `json:"exec_ms_median"`

	// Throughput.
	TPS    float64 `json:"tps"`
	MgasPS float64 `json:"mgas_per_s"`

	// Per-tx latency distribution (ms) computed from per-tx gas / total time.
	// We approximate per-tx time as (wall_ms / N) — accurate enough for
	// batched throughput-style benchmarks.
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`

	// Total gas reported by the backend (median across runs).
	TotalGas uint64 `json:"total_gas"`

	// Block-STM diagnostics (if any).
	Conflicts    uint32 `json:"conflicts,omitempty"`
	ReExecutions uint32 `json:"re_executions,omitempty"`
}

// runOne executes one workload + n + backend combo, doing a warmup followed
// by `runs` measurement repetitions. Returns CellResult with median stats.
//
// If the backend isn't available, returns a CellResult with Skipped=true.
func runOne(workload *Workload, n, runs, warmupN int, backend cevm.Backend, available map[cevm.Backend]bool) CellResult {
	res := CellResult{
		Workload: workload.Name,
		Backend:  cevm.BackendName(backend),
		N:        n,
		Runs:     runs,
	}

	if !available[backend] {
		res.Skipped = true
		res.SkipReason = "backend not available on this machine"
		return res
	}

	// Warmup: execute warmupN txs once to amortize GPU init / JIT.
	if warmupN > 0 {
		warmup := workload.Gen(warmupN)
		_, err := cevm.ExecuteBlock(backend, warmup)
		if err != nil {
			res.Skipped = true
			res.SkipReason = fmt.Sprintf("warmup failed: %v", err)
			return res
		}
	}

	// Generate the batch ONCE — same bytes every run for fairness.
	txs := workload.Gen(n)

	wallMs := make([]float64, 0, runs)
	execMs := make([]float64, 0, runs)
	gasUsed := make([]uint64, 0, runs)
	confl := uint32(0)
	reex := uint32(0)

	for r := 0; r < runs; r++ {
		t0 := time.Now()
		br, err := cevm.ExecuteBlock(backend, txs)
		dt := time.Since(t0)
		if err != nil {
			res.Skipped = true
			res.SkipReason = fmt.Sprintf("execute failed run %d: %v", r, err)
			return res
		}
		wallMs = append(wallMs, float64(dt.Microseconds())/1000.0)
		execMs = append(execMs, br.ExecTimeMs)
		gasUsed = append(gasUsed, br.TotalGas)
		if br.Conflicts > confl {
			confl = br.Conflicts
		}
		if br.ReExecutions > reex {
			reex = br.ReExecutions
		}
	}

	res.WallMsMedian = median(wallMs)
	res.WallMsStdDev = stddev(wallMs)
	res.ExecMsMedian = median(execMs)
	res.TotalGas = medianU64(gasUsed)

	wallS := res.WallMsMedian / 1000.0
	if wallS > 0 {
		res.TPS = float64(n) / wallS
		res.MgasPS = float64(res.TotalGas) / 1e6 / wallS
	}
	// Latency distribution. The cevm API returns one wall-clock per block,
	// so per-tx P50/P95/P99 are derived from the spread across the `runs`
	// repetitions (block-level latency), then scaled to per-tx by dividing
	// by N. This captures variance run-to-run, not within-batch variance.
	res.P50 = percentile(wallMs, 0.50) / float64(n)
	res.P95 = percentile(wallMs, 0.95) / float64(n)
	res.P99 = percentile(wallMs, 0.99) / float64(n)

	res.Conflicts = confl
	res.ReExecutions = reex
	return res
}

// median returns the median of xs (xs is mutated).
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func medianU64(xs []uint64) uint64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]uint64, len(xs))
	copy(cp, xs)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp[len(cp)/2]
}

// percentile returns the p-th percentile of xs (0 <= p <= 1).
// Linear interpolation between order statistics.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	if p <= 0 {
		return cp[0]
	}
	if p >= 1 {
		return cp[len(cp)-1]
	}
	pos := p * float64(len(cp)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(cp) {
		return cp[len(cp)-1]
	}
	frac := pos - float64(lo)
	return cp[lo]*(1-frac) + cp[hi]*frac
}

func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)-1))
}
