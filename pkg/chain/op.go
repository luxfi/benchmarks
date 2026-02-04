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

// OP implements Chain for OP Stack (Optimism)
type OP struct {
	client  *ethclient.Client
	chainID *big.Int
	key     *ecdsa.PrivateKey
	addr    common.Address
	nonce   uint64
	mu      sync.Mutex
}

func NewOP() Chain {
	return &OP{}
}

func (o *OP) Name() string {
	return "op-stack"
}

func (o *OP) Connect(ctx context.Context) error {
	endpoint := getEnvOrDefault("OP_ENDPOINT", "http://localhost:8546")
	client, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	o.client = client

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	o.chainID = chainID

	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	o.key = key
	o.addr = common.PubkeyToAddress(key.PublicKey)

	nonce, err := client.PendingNonceAt(ctx, o.addr)
	if err != nil {
		return err
	}
	o.nonce = nonce

	return nil
}

func (o *OP) Disconnect() {
	if o.client != nil {
		o.client.Close()
	}
}

func (o *OP) SendTransactions(ctx context.Context, duration time.Duration, concurrency int, workload string, collector *metrics.Collector) (int, error) {
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

				if err := o.sendTx(ctx); err != nil {
					continue
				}
				txCount.Add(1)
			}
		}()
	}

	wg.Wait()
	return int(txCount.Load()), nil
}

func (o *OP) sendTx(ctx context.Context) error {
	o.mu.Lock()
	nonce := o.nonce
	o.nonce++
	o.mu.Unlock()

	gasPrice, err := o.client.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}

	tx := types.NewTransaction(nonce, o.addr, big.NewInt(0), 21000, gasPrice, nil)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(o.chainID), o.key)
	if err != nil {
		return err
	}

	return o.client.SendTransaction(ctx, signedTx)
}

func (o *OP) MeasureLatency(ctx context.Context, duration time.Duration, workload string) ([]time.Duration, error) {
	var latencies []time.Duration
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return latencies, ctx.Err()
		default:
		}

		start := time.Now()

		o.mu.Lock()
		nonce := o.nonce
		o.nonce++
		o.mu.Unlock()

		gasPrice, _ := o.client.SuggestGasPrice(ctx)
		tx := types.NewTransaction(nonce, o.addr, big.NewInt(0), 21000, gasPrice, nil)
		signedTx, _ := types.SignTx(tx, types.NewEIP155Signer(o.chainID), o.key)

		if err := o.client.SendTransaction(ctx, signedTx); err != nil {
			continue
		}

		for {
			receipt, err := o.client.TransactionReceipt(ctx, signedTx.Hash())
			if err == nil && receipt != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		latencies = append(latencies, time.Since(start))
	}

	return latencies, nil
}

func (o *OP) MeasureMemory(ctx context.Context, duration time.Duration) (*metrics.MemoryStats, error) {
	return metrics.MeasureContainerMemory(ctx, "op-geth", duration)
}

func (o *OP) MeasureQueryPerformance(ctx context.Context, duration time.Duration, concurrency int) (*metrics.QueryStats, error) {
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
				_, err := o.client.BlockNumber(ctx)
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
