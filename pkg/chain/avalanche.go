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

type Avalanche struct {
	client  *ethclient.Client
	chainID *big.Int
	key     *ecdsa.PrivateKey
	addr    common.Address
	nonce   uint64
	mu      sync.Mutex
}

func NewAvalanche() Chain {
	return &Avalanche{}
}

func (a *Avalanche) Name() string {
	return "avalanche"
}

func (a *Avalanche) Connect(ctx context.Context) error {
	endpoint := getEnvOrDefault("AVALANCHE_ENDPOINT", "http://localhost:9660/ext/bc/C/rpc")
	client, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	a.client = client

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	a.chainID = chainID

	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	a.key = key
	a.addr = common.PubkeyToAddress(key.PublicKey)

	nonce, err := client.PendingNonceAt(ctx, a.addr)
	if err != nil {
		return err
	}
	a.nonce = nonce

	return nil
}

func (a *Avalanche) Disconnect() {
	if a.client != nil {
		a.client.Close()
	}
}

func (a *Avalanche) SendTransactions(ctx context.Context, duration time.Duration, concurrency int, workload string, collector *metrics.Collector) (int, error) {
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

				if err := a.sendTx(ctx); err != nil {
					continue
				}
				txCount.Add(1)
			}
		}()
	}

	wg.Wait()
	return int(txCount.Load()), nil
}

func (a *Avalanche) sendTx(ctx context.Context) error {
	a.mu.Lock()
	nonce := a.nonce
	a.nonce++
	a.mu.Unlock()

	gasPrice, err := a.client.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}

	tx := types.NewTransaction(nonce, a.addr, big.NewInt(0), 21000, gasPrice, nil)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(a.chainID), a.key)
	if err != nil {
		return err
	}

	return a.client.SendTransaction(ctx, signedTx)
}

func (a *Avalanche) MeasureLatency(ctx context.Context, duration time.Duration, workload string) ([]time.Duration, error) {
	var latencies []time.Duration
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return latencies, ctx.Err()
		default:
		}

		start := time.Now()

		a.mu.Lock()
		nonce := a.nonce
		a.nonce++
		a.mu.Unlock()

		gasPrice, _ := a.client.SuggestGasPrice(ctx)
		tx := types.NewTransaction(nonce, a.addr, big.NewInt(0), 21000, gasPrice, nil)
		signedTx, _ := types.SignTx(tx, types.NewEIP155Signer(a.chainID), a.key)

		if err := a.client.SendTransaction(ctx, signedTx); err != nil {
			continue
		}

		for {
			receipt, err := a.client.TransactionReceipt(ctx, signedTx.Hash())
			if err == nil && receipt != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		latencies = append(latencies, time.Since(start))
	}

	return latencies, nil
}

func (a *Avalanche) MeasureMemory(ctx context.Context, duration time.Duration) (*metrics.MemoryStats, error) {
	return metrics.MeasureContainerMemory(ctx, "avalanche-node", duration)
}

func (a *Avalanche) MeasureQueryPerformance(ctx context.Context, duration time.Duration, concurrency int) (*metrics.QueryStats, error) {
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
				_, err := a.client.BlockNumber(ctx)
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
