// Copyright (C) 2019-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// gpu-bench measures Lux cevm performance across realistic workloads,
// every backend exposed by libevm-gpu, and a sweep of batch sizes.
//
// Build:
//   CGO_ENABLED=1 go build -tags cgo -o bin/gpu-bench ./cmd/gpu-bench
//
// Examples:
//   gpu-bench --workload=all --n=sweep --runs=5 --out=text
//   gpu-bench --workload=ERC20Transfer --n=2048 --runs=3 --out=json --output=results.json
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"flag"

	"github.com/luxfi/cevm"
)

const (
	defaultWarmupN = 16
	defaultRuns    = 5
)

var defaultSweep = []int{32, 128, 512, 2048, 8192}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gpu-bench", flag.ContinueOnError)
	fs.SetOutput(stderr)

	workloadName := fs.String("workload", "all", "workload name or 'all' (Compute|ERC20Transfer|AMM|Keccak|Storage|NFT)")
	nFlag := fs.String("n", "sweep", "tx count: integer or 'sweep' for "+sweepStr())
	runsFlag := fs.Int("runs", defaultRuns, "measurement repeats per cell (median reported)")
	warmupFlag := fs.Int("warmup", defaultWarmupN, "warmup tx count (per backend, per cell)")
	outFlag := fs.String("out", "text", "output format: text|json")
	outputFlag := fs.String("output", "", "output file (default: stdout)")
	listFlag := fs.Bool("list-workloads", false, "list available workloads and exit")
	noCUDAFlag := fs.Bool("no-cuda", false, "skip GPUCUDA even if available")
	noMetalFlag := fs.Bool("no-metal", false, "skip GPUMetal even if available")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *listFlag {
		fmt.Fprintln(stdout, "Available workloads:")
		fmt.Fprint(stdout, describeWorkloads())
		return nil
	}

	// Resolve N values.
	var ns []int
	if strings.EqualFold(*nFlag, "sweep") {
		ns = append(ns, defaultSweep...)
	} else {
		v, err := strconv.Atoi(*nFlag)
		if err != nil || v < 1 {
			return fmt.Errorf("invalid --n=%q (want integer >=1 or 'sweep')", *nFlag)
		}
		ns = []int{v}
	}

	// Resolve workloads.
	var wls []*Workload
	if strings.EqualFold(*workloadName, "all") {
		wls = allWorkloads
	} else {
		w := workloadByName(*workloadName)
		if w == nil {
			return fmt.Errorf("unknown workload %q (try --list-workloads)", *workloadName)
		}
		wls = []*Workload{w}
	}

	// Discover backends.
	available := make(map[cevm.Backend]bool)
	for _, b := range cevm.AvailableBackends() {
		available[b] = true
	}
	if *noCUDAFlag {
		available[cevm.GPUCUDA] = false
	}
	if *noMetalFlag {
		available[cevm.GPUMetal] = false
	}
	// CPU-Sequential is always available.
	available[cevm.CPUSequential] = true

	backends := []cevm.Backend{cevm.CPUSequential, cevm.CPUParallel, cevm.GPUMetal, cevm.GPUCUDA}

	// Build the report skeleton.
	r := &Report{
		Hardware:    hostInfo(),
		GeneratedAt: time.Now().UTC(),
		CevmABI:     cevm.LibraryABIVersion(),
		GitSHA:      gitSHA(),
		Backends:    backendNames(cevm.AvailableBackends()),
		WarmupN:     *warmupFlag,
		Runs:        *runsFlag,
	}
	if h, _ := os.Hostname(); h != "" {
		r.Hardware.Hostname = h
	}

	totalCells := len(wls) * len(ns) * len(backends)
	cellNum := 0
	tStart := time.Now()
	for _, w := range wls {
		for _, n := range ns {
			for _, b := range backends {
				cellNum++
				skipMark := ""
				if !available[b] {
					skipMark = " [SKIP]"
				}
				fmt.Fprintf(stderr, "[%d/%d] %s | N=%d | %s%s\n",
					cellNum, totalCells, w.Name, n, cevm.BackendName(b), skipMark)
				cell := runOne(w, n, *runsFlag, *warmupFlag, b, available)
				r.Cells = append(r.Cells, cell)
			}
		}
	}
	fmt.Fprintf(stderr, "Done in %s.\n", time.Since(tStart).Round(time.Millisecond))

	// Output.
	var sink io.Writer = stdout
	var f *os.File
	if *outputFlag != "" {
		of, err := os.Create(*outputFlag)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		f = of
		sink = of
		defer of.Close()
	}

	switch strings.ToLower(*outFlag) {
	case "json":
		return renderJSON(sink, r)
	case "text":
		var buf bytes.Buffer
		renderText(&buf, r)
		_, err := sink.Write(buf.Bytes())
		return err
	default:
		_ = f
		return fmt.Errorf("invalid --out=%q (want text|json)", *outFlag)
	}
}

func sweepStr() string {
	parts := make([]string, len(defaultSweep))
	for i, v := range defaultSweep {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func backendNames(bs []cevm.Backend) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = cevm.BackendName(b)
	}
	return out
}

// gitSHA returns the short commit SHA of the lux repo, if we can find one.
// Returns "" when not in a git repo or git isn't on PATH.
func gitSHA() string {
	cmd := exec.Command("git", "rev-parse", "--short=12", "HEAD")
	cmd.Dir = "."
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}
