// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"context"
	"errors"
	"time"

	"github.com/luxfi/benchmarks/pkg/metrics"
)

// ErrSolanaNotImplemented indicates Solana benchmarks are not yet available.
// Solana uses a non-EVM architecture with different RPC (JSON-RPC 2.0),
// transaction format (Borsh serialization), and signing (Ed25519).
// Full implementation requires github.com/gagliardetto/solana-go or similar.
var ErrSolanaNotImplemented = errors.New("solana: not implemented (non-EVM chain requires dedicated SDK)")

// Solana implements Chain interface as a stub.
// Solana is not EVM-compatible and requires a separate implementation
// using the Solana RPC client and transaction primitives.
type Solana struct {
	endpoint string
}

func NewSolana() Chain {
	return &Solana{}
}

func (s *Solana) Name() string {
	return "solana"
}

func (s *Solana) Connect(ctx context.Context) error {
	s.endpoint = getEnvOrDefault("SOLANA_ENDPOINT", "http://localhost:8899")
	// Connection would require solana-go client initialization
	return ErrSolanaNotImplemented
}

func (s *Solana) Disconnect() {
	// No-op for stub
}

func (s *Solana) SendTransactions(ctx context.Context, duration time.Duration, concurrency int, workload string, collector *metrics.Collector) (int, error) {
	return 0, ErrSolanaNotImplemented
}

func (s *Solana) MeasureLatency(ctx context.Context, duration time.Duration, workload string) ([]time.Duration, error) {
	return nil, ErrSolanaNotImplemented
}

func (s *Solana) MeasureMemory(ctx context.Context, duration time.Duration) (*metrics.MemoryStats, error) {
	// Memory measurement via Docker still works for container monitoring
	return metrics.MeasureContainerMemory(ctx, "solana-validator", duration)
}

func (s *Solana) MeasureQueryPerformance(ctx context.Context, duration time.Duration, concurrency int) (*metrics.QueryStats, error) {
	return nil, ErrSolanaNotImplemented
}
