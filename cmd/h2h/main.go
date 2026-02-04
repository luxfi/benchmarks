// Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// h2h runs a head-to-head C-chain block creation benchmark between
// Lux and Avalanche 5-node networks.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luxfi/geth/common"
	"github.com/luxfi/geth/core/types"
	"github.com/luxfi/geth/crypto"
	"github.com/luxfi/geth/rlp"
)

// Network configuration
type Network struct {
	Name     string
	Endpoint string
	Key      *ecdsa.PrivateKey
	Addr     common.Address
	ChainID  *big.Int
}

func main() {
	ctx := context.Background()

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║     LUX vs AVALANCHE: 5-Node C-Chain Block Creation        ║")
	fmt.Println("║                Head-to-Head Benchmark                       ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Set up Lux network (Hardhat Account 0)
	luxKey := mustParseKey("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	lux := &Network{
		Name:     "Lux",
		Endpoint: "http://127.0.0.1:9652/ext/bc/C/rpc",
		Key:      luxKey,
		Addr:     crypto.PubkeyToAddress(luxKey.PublicKey),
	}

	// Set up Avalanche network (local network pre-funded key)
	avaxKey := mustParseKey("REDACTED_EWOQ_KEY")
	avax := &Network{
		Name:     "Avalanche",
		Endpoint: "http://127.0.0.1:19650/ext/bc/C/rpc",
		Key:      avaxKey,
		Addr:     crypto.PubkeyToAddress(avaxKey.PublicKey),
	}

	// Get chain IDs
	fmt.Println("Connecting to networks...")
	lux.ChainID = mustGetChainID(ctx, lux.Endpoint)
	avax.ChainID = mustGetChainID(ctx, avax.Endpoint)
	fmt.Printf("  Lux chain ID:       %s (addr: %s)\n", lux.ChainID, lux.Addr.Hex())
	fmt.Printf("  Avalanche chain ID: %s (addr: %s)\n", avax.ChainID, avax.Addr.Hex())

	// Check balances
	luxBal := getBalance(ctx, lux.Endpoint, lux.Addr)
	avaxBal := getBalance(ctx, avax.Endpoint, avax.Addr)
	fmt.Printf("  Lux balance:       %s wei\n", luxBal)
	fmt.Printf("  Avalanche balance: %s wei\n", avaxBal)
	fmt.Println()

	if luxBal.Sign() == 0 {
		fmt.Println("ERROR: Lux account has no balance. Check genesis pre-funded accounts.")
		return
	}
	if avaxBal.Sign() == 0 {
		fmt.Println("ERROR: Avalanche account has no balance. Check genesis pre-funded accounts.")
		return
	}

	// Run benchmarks
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  BENCHMARK 1: Block Creation Speed (30 seconds)")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	luxResult := benchmarkBlockCreation(ctx, lux, 30*time.Second, 10)
	avaxResult := benchmarkBlockCreation(ctx, avax, 30*time.Second, 10)

	printComparison("Block Creation", luxResult, avaxResult)

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  BENCHMARK 2: Transaction Throughput (30 seconds, 20 senders)")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	luxTPS := benchmarkThroughput(ctx, lux, 30*time.Second, 20)
	avaxTPS := benchmarkThroughput(ctx, avax, 30*time.Second, 20)

	printTPSComparison(luxTPS, avaxTPS)

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  BENCHMARK 3: Transaction Latency (10 sequential txs)")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	luxLatencies := benchmarkLatency(ctx, lux, 10)
	avaxLatencies := benchmarkLatency(ctx, avax, 10)

	printLatencyComparison(luxLatencies, avaxLatencies)

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  FINAL RESULTS SUMMARY")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("  %-25s %15s %15s %10s\n", "Metric", "Lux", "Avalanche", "Winner")
	fmt.Printf("  %-25s %15s %15s %10s\n", strings.Repeat("─", 25), strings.Repeat("─", 15), strings.Repeat("─", 15), strings.Repeat("─", 10))
	fmt.Printf("  %-25s %15d %15d %10s\n", "Blocks Created", luxResult.BlocksCreated, avaxResult.BlocksCreated, winner(luxResult.BlocksCreated, avaxResult.BlocksCreated))
	fmt.Printf("  %-25s %12.2f ms %12.2f ms %10s\n", "Avg Block Time", luxResult.AvgBlockTime.Seconds()*1000, avaxResult.AvgBlockTime.Seconds()*1000, winnerLow(luxResult.AvgBlockTime, avaxResult.AvgBlockTime))
	fmt.Printf("  %-25s %15d %15d %10s\n", "Txs Submitted", luxTPS.TxsSent, avaxTPS.TxsSent, winner(luxTPS.TxsSent, avaxTPS.TxsSent))
	fmt.Printf("  %-25s %12.2f %12.2f %10s\n", "Effective TPS", luxTPS.TPS, avaxTPS.TPS, winnerFloat(luxTPS.TPS, avaxTPS.TPS))
	if len(luxLatencies) > 0 && len(avaxLatencies) > 0 {
		luxP50 := percentile(luxLatencies, 50)
		avaxP50 := percentile(avaxLatencies, 50)
		fmt.Printf("  %-25s %12.2f ms %12.2f ms %10s\n", "P50 Latency", luxP50.Seconds()*1000, avaxP50.Seconds()*1000, winnerLow(luxP50, avaxP50))
	}
	fmt.Println()
}

type BlockResult struct {
	BlocksCreated int
	TxsInBlocks   int
	AvgBlockTime  time.Duration
	MinBlockTime  time.Duration
	MaxBlockTime  time.Duration
	Duration      time.Duration
}

func benchmarkBlockCreation(ctx context.Context, net *Network, duration time.Duration, concurrency int) BlockResult {
	fmt.Printf("  [%s] Starting block creation benchmark (%d senders, %v)...\n", net.Name, concurrency, duration)

	// Get starting block number
	startBlock := getBlockNumber(ctx, net.Endpoint)
	startNonce := getNonce(ctx, net.Endpoint, net.Addr)

	// Track block times
	var blockTimes []time.Duration
	var mu sync.Mutex

	// Start block monitor
	monitorCtx, monitorCancel := context.WithCancel(ctx)
	defer monitorCancel()

	go func() {
		lastBlock := startBlock
		lastTime := time.Now()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				current := getBlockNumber(ctx, net.Endpoint)
				if current > lastBlock {
					now := time.Now()
					blocksAdvanced := current - lastBlock
					for i := uint64(0); i < blocksAdvanced; i++ {
						bt := now.Sub(lastTime) / time.Duration(blocksAdvanced)
						mu.Lock()
						blockTimes = append(blockTimes, bt)
						mu.Unlock()
					}
					lastBlock = current
					lastTime = now
				}
			}
		}
	}()

	// Send transactions concurrently
	var wg sync.WaitGroup
	var txCount atomic.Int64
	var errCount atomic.Int64
	deadline := time.Now().Add(duration)
	nonce := atomic.Uint64{}
	nonce.Store(startNonce)

	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signer := types.NewEIP155Signer(net.ChainID)
			gasPrice := big.NewInt(50_000_000_000) // 50 gwei

			for time.Now().Before(deadline) {
				n := nonce.Add(1) - 1
				tx := types.NewTransaction(n, net.Addr, big.NewInt(0), 21000, gasPrice, nil)
				signedTx, err := types.SignTx(tx, signer, net.Key)
				if err != nil {
					errCount.Add(1)
					continue
				}

				if err := sendRawTx(ctx, net.Endpoint, signedTx); err != nil {
					errCount.Add(1)
					continue
				}
				txCount.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	monitorCancel()

	// Wait for remaining blocks to be produced
	time.Sleep(2 * time.Second)
	endBlock := getBlockNumber(ctx, net.Endpoint)

	mu.Lock()
	times := make([]time.Duration, len(blockTimes))
	copy(times, blockTimes)
	mu.Unlock()

	blocksCreated := int(endBlock - startBlock)
	sent := txCount.Load()
	errs := errCount.Load()

	var avgBlockTime, minBlockTime, maxBlockTime time.Duration
	if len(times) > 0 {
		var total time.Duration
		minBlockTime = times[0]
		maxBlockTime = times[0]
		for _, t := range times {
			total += t
			if t < minBlockTime {
				minBlockTime = t
			}
			if t > maxBlockTime {
				maxBlockTime = t
			}
		}
		avgBlockTime = total / time.Duration(len(times))
	}

	fmt.Printf("  [%s] Blocks: %d, Txs sent: %d, Errors: %d, Duration: %v\n",
		net.Name, blocksCreated, sent, errs, elapsed.Round(time.Millisecond))
	if len(times) > 0 {
		fmt.Printf("  [%s] Block time: avg=%v, min=%v, max=%v\n",
			net.Name, avgBlockTime.Round(time.Millisecond), minBlockTime.Round(time.Millisecond), maxBlockTime.Round(time.Millisecond))
	}

	return BlockResult{
		BlocksCreated: blocksCreated,
		TxsInBlocks:   int(sent),
		AvgBlockTime:  avgBlockTime,
		MinBlockTime:  minBlockTime,
		MaxBlockTime:  maxBlockTime,
		Duration:      elapsed,
	}
}

type TPSResult struct {
	TxsSent  int
	TPS      float64
	Duration time.Duration
}

func benchmarkThroughput(ctx context.Context, net *Network, duration time.Duration, concurrency int) TPSResult {
	fmt.Printf("  [%s] Starting throughput benchmark (%d senders, %v)...\n", net.Name, concurrency, duration)

	startNonce := getNonce(ctx, net.Endpoint, net.Addr)
	nonce := atomic.Uint64{}
	nonce.Store(startNonce)

	var wg sync.WaitGroup
	var txCount atomic.Int64
	deadline := time.Now().Add(duration)
	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signer := types.NewEIP155Signer(net.ChainID)
			gasPrice := big.NewInt(50_000_000_000)

			for time.Now().Before(deadline) {
				n := nonce.Add(1) - 1
				tx := types.NewTransaction(n, net.Addr, big.NewInt(0), 21000, gasPrice, nil)
				signedTx, err := types.SignTx(tx, signer, net.Key)
				if err != nil {
					continue
				}
				if err := sendRawTx(ctx, net.Endpoint, signedTx); err != nil {
					continue
				}
				txCount.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	sent := int(txCount.Load())
	tps := float64(sent) / elapsed.Seconds()

	fmt.Printf("  [%s] Sent: %d txs, TPS: %.2f\n", net.Name, sent, tps)

	return TPSResult{
		TxsSent:  sent,
		TPS:      tps,
		Duration: elapsed,
	}
}

func benchmarkLatency(ctx context.Context, net *Network, count int) []time.Duration {
	fmt.Printf("  [%s] Starting latency benchmark (%d sequential txs)...\n", net.Name, count)

	startNonce := getNonce(ctx, net.Endpoint, net.Addr)
	signer := types.NewEIP155Signer(net.ChainID)
	gasPrice := big.NewInt(50_000_000_000)
	var latencies []time.Duration

	for i := 0; i < count; i++ {
		n := startNonce + uint64(i)
		tx := types.NewTransaction(n, net.Addr, big.NewInt(0), 21000, gasPrice, nil)
		signedTx, err := types.SignTx(tx, signer, net.Key)
		if err != nil {
			fmt.Printf("  [%s] Sign error: %v\n", net.Name, err)
			continue
		}

		start := time.Now()
		if err := sendRawTx(ctx, net.Endpoint, signedTx); err != nil {
			fmt.Printf("  [%s] Send error: %v\n", net.Name, err)
			continue
		}

		// Poll for receipt
		txHash := signedTx.Hash()
		for {
			receipt := getReceipt(ctx, net.Endpoint, txHash)
			if receipt != "" {
				break
			}
			time.Sleep(50 * time.Millisecond)
			if time.Since(start) > 30*time.Second {
				fmt.Printf("  [%s] Timeout waiting for tx %d\n", net.Name, i)
				break
			}
		}

		latency := time.Since(start)
		latencies = append(latencies, latency)
	}

	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p50 := percentile(latencies, 50)
		p99 := percentile(latencies, 99)
		fmt.Printf("  [%s] P50: %v, P99: %v, Count: %d\n", net.Name, p50.Round(time.Millisecond), p99.Round(time.Millisecond), len(latencies))
	}

	return latencies
}

// JSON-RPC helpers

type jsonrpcReq struct {
	Jsonrpc string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type jsonrpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func rpcCall(ctx context.Context, endpoint, method string, params ...interface{}) (json.RawMessage, error) {
	req := jsonrpcReq{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}
	body, _ := json.Marshal(req)

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result jsonrpcResp
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w (body: %s)", err, string(data))
	}
	if result.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", result.Error.Code, result.Error.Message)
	}
	return result.Result, nil
}

func mustGetChainID(ctx context.Context, endpoint string) *big.Int {
	result, err := rpcCall(ctx, endpoint, "eth_chainId")
	if err != nil {
		panic(fmt.Sprintf("get chain ID from %s: %v", endpoint, err))
	}
	var hexStr string
	json.Unmarshal(result, &hexStr)
	id := new(big.Int)
	id.SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	return id
}

func getBalance(ctx context.Context, endpoint string, addr common.Address) *big.Int {
	result, err := rpcCall(ctx, endpoint, "eth_getBalance", addr.Hex(), "latest")
	if err != nil {
		return big.NewInt(0)
	}
	var hexStr string
	json.Unmarshal(result, &hexStr)
	bal := new(big.Int)
	bal.SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	return bal
}

func getBlockNumber(ctx context.Context, endpoint string) uint64 {
	result, err := rpcCall(ctx, endpoint, "eth_blockNumber")
	if err != nil {
		return 0
	}
	var hexStr string
	json.Unmarshal(result, &hexStr)
	n := new(big.Int)
	n.SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	return n.Uint64()
}

func getNonce(ctx context.Context, endpoint string, addr common.Address) uint64 {
	result, err := rpcCall(ctx, endpoint, "eth_getTransactionCount", addr.Hex(), "pending")
	if err != nil {
		return 0
	}
	var hexStr string
	json.Unmarshal(result, &hexStr)
	n := new(big.Int)
	n.SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	return n.Uint64()
}

func sendRawTx(ctx context.Context, endpoint string, tx *types.Transaction) error {
	data, err := rlp.EncodeToBytes(tx)
	if err != nil {
		return err
	}
	_, err = rpcCall(ctx, endpoint, "eth_sendRawTransaction", "0x"+hex.EncodeToString(data))
	return err
}

func getReceipt(ctx context.Context, endpoint string, txHash common.Hash) string {
	result, err := rpcCall(ctx, endpoint, "eth_getTransactionReceipt", txHash.Hex())
	if err != nil || string(result) == "null" {
		return ""
	}
	return string(result)
}

func mustParseKey(hexKey string) *ecdsa.PrivateKey {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		panic(fmt.Sprintf("decode key hex: %v", err))
	}
	key, err := crypto.ToECDSA(keyBytes)
	if err != nil {
		panic(fmt.Sprintf("parse key: %v", err))
	}
	return key
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := len(sorted) * p / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func printComparison(name string, lux, avax BlockResult) {
	fmt.Printf("\n  %s Comparison:\n", name)
	fmt.Printf("  %-20s %12s %12s\n", "", "Lux", "Avalanche")
	fmt.Printf("  %-20s %12d %12d\n", "Blocks created", lux.BlocksCreated, avax.BlocksCreated)
	fmt.Printf("  %-20s %12v %12v\n", "Avg block time", lux.AvgBlockTime.Round(time.Millisecond), avax.AvgBlockTime.Round(time.Millisecond))
	fmt.Printf("  %-20s %12v %12v\n", "Min block time", lux.MinBlockTime.Round(time.Millisecond), avax.MinBlockTime.Round(time.Millisecond))
	fmt.Printf("  %-20s %12v %12v\n", "Max block time", lux.MaxBlockTime.Round(time.Millisecond), avax.MaxBlockTime.Round(time.Millisecond))
	fmt.Printf("  %-20s %12d %12d\n", "Txs submitted", lux.TxsInBlocks, avax.TxsInBlocks)
}

func printTPSComparison(lux, avax TPSResult) {
	fmt.Printf("\n  Throughput Comparison:\n")
	fmt.Printf("  %-20s %12s %12s\n", "", "Lux", "Avalanche")
	fmt.Printf("  %-20s %12d %12d\n", "Txs sent", lux.TxsSent, avax.TxsSent)
	fmt.Printf("  %-20s %12.2f %12.2f\n", "TPS", lux.TPS, avax.TPS)
	speedup := lux.TPS / avax.TPS
	if avax.TPS > 0 {
		fmt.Printf("  Lux speedup: %.2fx\n", speedup)
	}
}

func printLatencyComparison(luxL, avaxL []time.Duration) {
	if len(luxL) == 0 || len(avaxL) == 0 {
		fmt.Println("  Insufficient data for latency comparison")
		return
	}
	fmt.Printf("\n  Latency Comparison:\n")
	fmt.Printf("  %-20s %12s %12s\n", "", "Lux", "Avalanche")
	fmt.Printf("  %-20s %12v %12v\n", "P50", percentile(luxL, 50).Round(time.Millisecond), percentile(avaxL, 50).Round(time.Millisecond))
	fmt.Printf("  %-20s %12v %12v\n", "P95", percentile(luxL, 95).Round(time.Millisecond), percentile(avaxL, 95).Round(time.Millisecond))
	fmt.Printf("  %-20s %12v %12v\n", "P99", percentile(luxL, 99).Round(time.Millisecond), percentile(avaxL, 99).Round(time.Millisecond))
}

func winner(a, b int) string {
	if a > b {
		return "LUX"
	}
	if b > a {
		return "AVAX"
	}
	return "TIE"
}

func winnerLow(a, b time.Duration) string {
	if a < b {
		return "LUX"
	}
	if b < a {
		return "AVAX"
	}
	return "TIE"
}

func winnerFloat(a, b float64) string {
	if a > b {
		return "LUX"
	}
	if b > a {
		return "AVAX"
	}
	return "TIE"
}
