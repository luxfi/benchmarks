// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/luxfi/benchmarks/pkg/metrics"
)

// getEnvOrDefault returns environment variable value or default.
func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// Chain interface for blockchain implementations
type Chain interface {
	Name() string
	Connect(ctx context.Context) error
	Disconnect()

	// Transaction benchmarks
	SendTransactions(ctx context.Context, duration time.Duration, concurrency int, workload string, collector *metrics.Collector) (int, error)
	MeasureLatency(ctx context.Context, duration time.Duration, workload string) ([]time.Duration, error)

	// Resource benchmarks
	MeasureMemory(ctx context.Context, duration time.Duration) (*metrics.MemoryStats, error)
	MeasureQueryPerformance(ctx context.Context, duration time.Duration, concurrency int) (*metrics.QueryStats, error)
}

// Registry of chain implementations
var registry = make(map[string]func() Chain)

func Register(name string, factory func() Chain) {
	registry[name] = factory
}

func Get(name string) (Chain, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown chain: %s", name)
	}
	return factory(), nil
}

func init() {
	Register("lux", NewLux)
	Register("avalanche", NewAvalanche)
	Register("geth", NewGeth)
	Register("op", NewOP)
	Register("solana", NewSolana)
}
