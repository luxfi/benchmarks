// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package metrics

import (
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Collector struct {
	mu      sync.Mutex
	samples []Sample
}

type Sample struct {
	Timestamp time.Time
	Metric    string
	Value     float64
	Labels    map[string]string
}

func NewCollector() *Collector {
	return &Collector{
		samples: make([]Sample, 0),
	}
}

func (c *Collector) Record(metric string, value float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.samples = append(c.samples, Sample{
		Timestamp: time.Now(),
		Metric:    metric,
		Value:     value,
		Labels:    labels,
	})
}

func (c *Collector) Samples() []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Sample{}, c.samples...)
}

type LatencyStatsResult struct {
	P50 float64
	P95 float64
	P99 float64
	Min float64
	Max float64
	Avg float64
}

func LatencyStats(latencies []time.Duration) LatencyStatsResult {
	if len(latencies) == 0 {
		return LatencyStatsResult{}
	}

	// Convert to milliseconds
	ms := make([]float64, len(latencies))
	var sum float64
	for i, l := range latencies {
		ms[i] = float64(l.Nanoseconds()) / 1e6
		sum += ms[i]
	}

	sort.Float64s(ms)

	return LatencyStatsResult{
		P50: percentile(ms, 50),
		P95: percentile(ms, 95),
		P99: percentile(ms, 99),
		Min: ms[0],
		Max: ms[len(ms)-1],
		Avg: sum / float64(len(ms)),
	}
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

type MemoryStats struct {
	Avg  uint64
	Peak uint64
	Min  uint64
}

type QueryStats struct {
	QPS        float64
	AvgLatency float64 // ms
}

// MeasureContainerMemory measures memory usage of a Docker container
func MeasureContainerMemory(ctx context.Context, containerName string, duration time.Duration) (*MemoryStats, error) {
	deadline := time.Now().Add(duration)
	var samples []uint64

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			break
		default:
		}

		// Get container stats
		cmd := exec.CommandContext(ctx, "docker", "stats", containerName, "--no-stream", "--format", "{{json .}}")
		output, err := cmd.Output()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		var stats struct {
			MemUsage string `json:"MemUsage"`
		}
		if err := json.Unmarshal(output, &stats); err != nil {
			time.Sleep(time.Second)
			continue
		}

		// Parse memory usage (format: "123MiB / 456MiB")
		parts := strings.Split(stats.MemUsage, " / ")
		if len(parts) > 0 {
			mem := parseMemory(parts[0])
			samples = append(samples, mem)
		}

		time.Sleep(time.Second)
	}

	if len(samples) == 0 {
		return &MemoryStats{}, nil
	}

	var sum, peak, min uint64
	min = samples[0]
	for _, s := range samples {
		sum += s
		if s > peak {
			peak = s
		}
		if s < min {
			min = s
		}
	}

	return &MemoryStats{
		Avg:  sum / uint64(len(samples)),
		Peak: peak,
		Min:  min,
	}, nil
}

func parseMemory(s string) uint64 {
	s = strings.TrimSpace(s)
	var multiplier uint64 = 1

	if strings.HasSuffix(s, "GiB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GiB")
	} else if strings.HasSuffix(s, "MiB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MiB")
	} else if strings.HasSuffix(s, "KiB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KiB")
	}

	val, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return uint64(val * float64(multiplier))
}
