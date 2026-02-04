// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luxfi/crypto"
	"github.com/luxfi/geth/common"
	"github.com/luxfi/geth/core/types"
	"github.com/luxfi/geth/ethclient"

	"github.com/luxfi/benchmarks/pkg/metrics"
)

type Geth struct {
	client  *ethclient.Client
	chainID *big.Int
	key     *ecdsa.PrivateKey
	addr    common.Address
	nonce   uint64
	mu      sync.Mutex
}

func NewGeth() Chain {
	return &Geth{}
}

func (g *Geth) Name() string {
	return "geth"
}

func (g *Geth) Connect(ctx context.Context) error {
	endpoint := getEnvOrDefault("GETH_ENDPOINT", "http://localhost:8545")
	client, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	g.client = client

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	g.chainID = chainID

	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	g.key = key
	g.addr = common.PubkeyToAddress(key.PublicKey)

	nonce, err := client.PendingNonceAt(ctx, g.addr)
	if err != nil {
		return err
	}
	g.nonce = nonce

	return nil
}

func (g *Geth) Disconnect() {
	if g.client != nil {
		g.client.Close()
	}
}

func (g *Geth) SendTransactions(ctx context.Context, duration time.Duration, concurrency int, workload string, collector *metrics.Collector) (int, error) {
	var txCount atomic.Int64
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if err := g.sendTx(ctx); err != nil {
					continue
				}
				txCount.Add(1)
			}
		}()
	}

	wg.Wait()
	return int(txCount.Load()), nil
}

func (g *Geth) sendTx(ctx context.Context) error {
	g.mu.Lock()
	nonce := g.nonce
	g.nonce++
	g.mu.Unlock()

	gasPrice, err := g.client.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}

	tx := types.NewTransaction(nonce, g.addr, big.NewInt(0), 21000, gasPrice, nil)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(g.chainID), g.key)
	if err != nil {
		return err
	}

	return g.client.SendTransaction(ctx, signedTx)
}

func (g *Geth) MeasureLatency(ctx context.Context, duration time.Duration, workload string) ([]time.Duration, error) {
	var latencies []time.Duration
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return latencies, ctx.Err()
		default:
		}

		start := time.Now()

		g.mu.Lock()
		nonce := g.nonce
		g.nonce++
		g.mu.Unlock()

		gasPrice, _ := g.client.SuggestGasPrice(ctx)
		tx := types.NewTransaction(nonce, g.addr, big.NewInt(0), 21000, gasPrice, nil)
		signedTx, _ := types.SignTx(tx, types.NewEIP155Signer(g.chainID), g.key)

		if err := g.client.SendTransaction(ctx, signedTx); err != nil {
			continue
		}

		for {
			receipt, err := g.client.TransactionReceipt(ctx, signedTx.Hash())
			if err == nil && receipt != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		latencies = append(latencies, time.Since(start))
	}

	return latencies, nil
}

func (g *Geth) MeasureMemory(ctx context.Context, duration time.Duration) (*metrics.MemoryStats, error) {
	return metrics.MeasureContainerMemory(ctx, "geth-node", duration)
}

func (g *Geth) MeasureQueryPerformance(ctx context.Context, duration time.Duration, concurrency int) (*metrics.QueryStats, error) {
	var queryCount atomic.Int64
	var totalLatency atomic.Int64
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return
				default:
				}

				start := time.Now()
				_, err := g.client.BlockNumber(ctx)
				if err != nil {
					continue
				}
				totalLatency.Add(time.Since(start).Nanoseconds())
				queryCount.Add(1)
			}
		}()
	}

	wg.Wait()

	count := queryCount.Load()
	if count == 0 {
		return &metrics.QueryStats{}, nil
	}

	return &metrics.QueryStats{
		QPS:        float64(count) / duration.Seconds(),
		AvgLatency: float64(totalLatency.Load()) / float64(count) / 1e6,
	}, nil
}
