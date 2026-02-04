// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chain

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luxfi/coreth/ethclient"
	"github.com/luxfi/crypto"
	"github.com/luxfi/geth/common"
	"github.com/luxfi/geth/core/types"

	"github.com/luxfi/benchmarks/pkg/metrics"
)

type Lux struct {
	client  *ethclient.Client
	chainID *big.Int
	key     *ecdsa.PrivateKey
	addr    common.Address
	nonce   uint64
	mu      sync.Mutex
}

func NewLux() Chain {
	return &Lux{}
}

func (l *Lux) Name() string {
	return "lux"
}

func (l *Lux) Connect(ctx context.Context) error {
	endpoint := getEnvOrDefault("LUX_ENDPOINT", "http://localhost:9650/ext/bc/C/rpc")
	client, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	l.client = client

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	l.chainID = chainID

	// Use provided key or generate ephemeral one
	keyHex := getEnvOrDefault("LUX_PRIVATE_KEY", "")
	var key *ecdsa.PrivateKey
	if keyHex != "" {
		keyHex = strings.TrimPrefix(keyHex, "0x")
		keyBytes, err := hex.DecodeString(keyHex)
		if err != nil {
			return fmt.Errorf("invalid LUX_PRIVATE_KEY: %w", err)
		}
		key, err = crypto.ToECDSA(keyBytes)
		if err != nil {
			return fmt.Errorf("invalid LUX_PRIVATE_KEY: %w", err)
		}
	} else {
		key, err = crypto.GenerateKey()
		if err != nil {
			return err
		}
	}
	l.key = key
	l.addr = common.PubkeyToAddress(key.PublicKey)

	nonce, err := client.NonceAt(ctx, l.addr, nil)
	if err != nil {
		return err
	}
	l.nonce = nonce

	return nil
}

func (l *Lux) Disconnect() {
	if l.client != nil {
		l.client.Close()
	}
}

func (l *Lux) SendTransactions(ctx context.Context, duration time.Duration, concurrency int, workload string, collector *metrics.Collector) (int, error) {
	var txCount atomic.Int64
	deadline := time.Now().Add(duration)

	// Get pending nonce to continue after any queued transactions
	var result string
	if err := l.client.Client().CallContext(ctx, &result, "eth_getTransactionCount", l.addr.Hex(), "pending"); err != nil {
		return 0, fmt.Errorf("get pending nonce: %w", err)
	}
	nonce, err := strconv.ParseUint(result[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse nonce: %w", err)
	}
	l.nonce = nonce

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

				if err := l.sendTx(ctx); err != nil {
					continue
				}
				txCount.Add(1)
			}
		}()
	}

	wg.Wait()
	if errCount.Load() > 0 && getEnvOrDefault("LUX_DEBUG", "") != "" {
		if msg := lastErrMsg.Load(); msg != nil {
			fmt.Printf("    [debug] %d errors, last: %s\n", errCount.Load(), *msg)
		}
	}
	errCount.Store(0)
	return int(txCount.Load()), nil
}

var (
	errCount   atomic.Int64
	lastErrMsg atomic.Pointer[string]
)

func (l *Lux) sendTx(ctx context.Context) error {
	l.mu.Lock()
	nonce := l.nonce
	l.nonce++
	l.mu.Unlock()

	gasPrice, err := l.client.SuggestGasPrice(ctx)
	if err != nil {
		msg := fmt.Sprintf("gas price: %v", err)
		lastErrMsg.Store(&msg)
		errCount.Add(1)
		return err
	}

	tx := types.NewTransaction(
		nonce,
		l.addr, // self-transfer
		big.NewInt(0),
		21000,
		gasPrice,
		nil,
	)

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(l.chainID), l.key)
	if err != nil {
		msg := fmt.Sprintf("sign: %v", err)
		lastErrMsg.Store(&msg)
		errCount.Add(1)
		return err
	}

	if err := l.client.SendTransaction(ctx, signedTx); err != nil {
		msg := fmt.Sprintf("send: %v", err)
		lastErrMsg.Store(&msg)
		errCount.Add(1)
		return err
	}
	return nil
}

func (l *Lux) MeasureLatency(ctx context.Context, duration time.Duration, workload string) ([]time.Duration, error) {
	var latencies []time.Duration
	var mu sync.Mutex
	deadline := time.Now().Add(duration)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return latencies, ctx.Err()
		default:
		}

		start := time.Now()

		l.mu.Lock()
		nonce := l.nonce
		l.nonce++
		l.mu.Unlock()

		gasPrice, err := l.client.SuggestGasPrice(ctx)
		if err != nil {
			continue
		}

		tx := types.NewTransaction(nonce, l.addr, big.NewInt(0), 21000, gasPrice, nil)
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(l.chainID), l.key)
		if err != nil {
			continue
		}

		if err := l.client.SendTransaction(ctx, signedTx); err != nil {
			continue
		}

		// Wait for receipt
		for {
			receipt, err := l.client.TransactionReceipt(ctx, signedTx.Hash())
			if err == nil && receipt != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		latency := time.Since(start)
		mu.Lock()
		latencies = append(latencies, latency)
		mu.Unlock()
	}

	return latencies, nil
}

func (l *Lux) MeasureMemory(ctx context.Context, duration time.Duration) (*metrics.MemoryStats, error) {
	// Memory measurement via Docker stats
	return metrics.MeasureContainerMemory(ctx, "lux-node", duration)
}

func (l *Lux) MeasureQueryPerformance(ctx context.Context, duration time.Duration, concurrency int) (*metrics.QueryStats, error) {
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
				_, err := l.client.BlockNumber(ctx)
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
		AvgLatency: float64(totalLatency.Load()) / float64(count) / 1e6, // ms
	}, nil
}
