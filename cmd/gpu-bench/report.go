// Copyright (C) 2019-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Report is the full benchmark output, ready to serialize as JSON.
type Report struct {
	Hardware    HardwareInfo `json:"hardware"`
	GeneratedAt time.Time    `json:"generated_at"`
	CevmABI     uint32       `json:"cevm_abi_version"`
	GitSHA      string       `json:"git_sha,omitempty"`
	Backends    []string     `json:"backends_available"`
	WarmupN     int          `json:"warmup_n"`
	Runs        int          `json:"runs_per_cell"`
	Cells       []CellResult `json:"cells"`
}

// HardwareInfo captures the box this ran on.
type HardwareInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	NumCPU   int    `json:"num_cpu"`
	GoVer    string `json:"go_version"`
	Hostname string `json:"hostname,omitempty"`
}

// renderText writes a human-readable text report grouped by (workload, n).
func renderText(w io.Writer, r *Report) {
	fmt.Fprintln(w, "=== GPU EVM Benchmark Report ===")
	fmt.Fprintf(w, "Hardware:   %s/%s, %d cores, Go %s\n", r.Hardware.OS, r.Hardware.Arch, r.Hardware.NumCPU, r.Hardware.GoVer)
	if r.Hardware.Hostname != "" {
		fmt.Fprintf(w, "Hostname:   %s\n", r.Hardware.Hostname)
	}
	fmt.Fprintf(w, "Date:       %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "cevm ABI:   v%d\n", r.CevmABI)
	if r.GitSHA != "" {
		fmt.Fprintf(w, "Git SHA:    %s\n", r.GitSHA)
	}
	fmt.Fprintf(w, "Backends:   %s\n", strings.Join(r.Backends, ", "))
	fmt.Fprintf(w, "Warmup N:   %d   Runs/cell: %d\n", r.WarmupN, r.Runs)
	fmt.Fprintln(w)

	// Group by (workload, n).
	type key struct {
		Workload string
		N        int
	}
	groups := make(map[key][]CellResult)
	keys := make([]key, 0)
	for _, c := range r.Cells {
		k := key{c.Workload, c.N}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], c)
	}
	// Sort keys by workload-order then by N ascending.
	wlOrder := func(name string) int {
		for i, w := range allWorkloads {
			if w.Name == name {
				return i
			}
		}
		return 999
	}
	sort.Slice(keys, func(i, j int) bool {
		if wlOrder(keys[i].Workload) != wlOrder(keys[j].Workload) {
			return wlOrder(keys[i].Workload) < wlOrder(keys[j].Workload)
		}
		return keys[i].N < keys[j].N
	})

	for _, k := range keys {
		fmt.Fprintf(w, "Workload: %s | N=%d\n", k.Workload, k.N)
		bar := strings.Repeat("─", 96)
		fmt.Fprintln(w, bar)
		fmt.Fprintf(w, "%-26s | %10s | %12s | %10s | %10s | %10s\n",
			"Backend", "Time(ms)", "TPS", "Mgas/s", "P95(ms)", "P99(ms)")
		fmt.Fprintln(w, bar)

		// Stable backend ordering: cpu-sequential, cpu-parallel, gpu-metal, gpu-cuda.
		// Backend names from the library may include suffixes ("cpu-parallel (Block-STM)"),
		// so we match by prefix.
		order := []string{"cpu-sequential", "cpu-parallel", "gpu-metal", "gpu-cuda"}
		rank := func(name string) int {
			for i, o := range order {
				if strings.HasPrefix(name, o) {
					return i
				}
			}
			return 100
		}
		cells := groups[k]
		sort.Slice(cells, func(i, j int) bool {
			return rank(cells[i].Backend) < rank(cells[j].Backend)
		})

		var bestGPU, parRow *CellResult
		for i := range cells {
			c := &cells[i]
			if c.Skipped {
				fmt.Fprintf(w, "%-26s | %10s | %12s | %10s | %10s | %10s   (%s)\n",
					c.Backend, "n/a", "n/a", "n/a", "n/a", "n/a", c.SkipReason)
				continue
			}
			fmt.Fprintf(w, "%-26s | %10.2f | %12.0f | %10.2f | %10.3f | %10.3f\n",
				c.Backend, c.WallMsMedian, c.TPS, c.MgasPS, c.P95, c.P99)
			if strings.HasPrefix(c.Backend, "gpu-") {
				if bestGPU == nil || c.TPS > bestGPU.TPS {
					bestGPU = c
				}
			}
			if strings.HasPrefix(c.Backend, "cpu-parallel") {
				parRow = c
			}
		}
		fmt.Fprintln(w, bar)
		if bestGPU != nil && parRow != nil && parRow.TPS > 0 {
			fmt.Fprintf(w, "GPU vs CPU-Par speedup: %.2fx\n", bestGPU.TPS/parRow.TPS)
		}
		fmt.Fprintln(w)
	}

	// Summary table: GPU speedup across (workload x n)
	fmt.Fprintln(w, "=== GPU-Metal vs CPU-Parallel Speedup Matrix ===")
	wls := make([]string, 0)
	seenWL := map[string]bool{}
	ns := make([]int, 0)
	seenN := map[int]bool{}
	for _, c := range r.Cells {
		if !seenWL[c.Workload] {
			seenWL[c.Workload] = true
			wls = append(wls, c.Workload)
		}
		if !seenN[c.N] {
			seenN[c.N] = true
			ns = append(ns, c.N)
		}
	}
	sort.Slice(wls, func(i, j int) bool { return wlOrder(wls[i]) < wlOrder(wls[j]) })
	sort.Ints(ns)

	fmt.Fprintf(w, "%-18s", "Workload")
	for _, n := range ns {
		fmt.Fprintf(w, " | N=%-7d", n)
	}
	fmt.Fprintln(w)
	bar := strings.Repeat("─", 18+len(ns)*13)
	fmt.Fprintln(w, bar)
	for _, wl := range wls {
		fmt.Fprintf(w, "%-18s", wl)
		for _, n := range ns {
			gpu, par := lookup(r.Cells, wl, n, "gpu-metal"), lookup(r.Cells, wl, n, "cpu-parallel")
			if gpu == nil || par == nil || gpu.Skipped || par.Skipped || par.TPS == 0 {
				fmt.Fprintf(w, " | %-9s", "n/a")
			} else {
				fmt.Fprintf(w, " | %7.2fx ", gpu.TPS/par.TPS)
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
}

func lookup(cells []CellResult, wl string, n int, backendPrefix string) *CellResult {
	for i := range cells {
		c := &cells[i]
		if c.Workload == wl && c.N == n && strings.HasPrefix(c.Backend, backendPrefix) {
			return c
		}
	}
	return nil
}

// renderJSON writes a machine-readable JSON report.
func renderJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// hostInfo builds a HardwareInfo for the current box.
func hostInfo() HardwareInfo {
	return HardwareInfo{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		NumCPU: runtime.NumCPU(),
		GoVer:  runtime.Version(),
	}
}
