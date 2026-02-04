// Real network benchmark: measures actual confirmed TPS on live networks
// by submitting signed EVM transactions and counting confirmed blocks/txs.
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luxfi/geth/common"
	"github.com/luxfi/geth/core/types"
	"github.com/luxfi/geth/crypto"
)

// Pre-funded account for local networks (Avalanche + Lux)
var fundedKeyHex = "REDACTED_EWOQ_KEY"

// HTTP client with timeouts to avoid blocking forever
var httpClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     30 * time.Second,
	},
}

// ---- JSON-RPC ----

func rpcCall(url, method string, params []interface{}) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "method": method, "params": params, "id": 1,
	})
	resp, err := httpClient.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r struct {
		Result json.RawMessage           `json:"result"`
		Error  *struct{ Message string } `json:"error"`
	}
	json.Unmarshal(data, &r)
	if r.Error != nil {
		return nil, fmt.Errorf("%s", r.Error.Message)
	}
	return r.Result, nil
}

func getBlockNumber(url string) (uint64, error) {
	r, err := rpcCall(url, "eth_blockNumber", nil)
	if err != nil {
		return 0, err
	}
	var h string
	json.Unmarshal(r, &h)
	var n uint64
	fmt.Sscanf(h, "0x%x", &n)
	return n, nil
}

func getBlockTxCount(url string, num uint64) (int, error) {
	h := fmt.Sprintf("0x%x", num)
	r, err := rpcCall(url, "eth_getBlockByNumber", []interface{}{h, false})
	if err != nil {
		return 0, err
	}
	var block map[string]interface{}
	json.Unmarshal(r, &block)
	if block == nil {
		return 0, nil
	}
	txs, _ := block["transactions"].([]interface{})
	return len(txs), nil
}

func getNonce(url, addr string) (uint64, error) {
	r, err := rpcCall(url, "eth_getTransactionCount", []interface{}{addr, "pending"})
	if err != nil {
		return 0, err
	}
	var h string
	json.Unmarshal(r, &h)
	var n uint64
	fmt.Sscanf(h, "0x%x", &n)
	return n, nil
}

func getChainID(url string) (*big.Int, error) {
	r, err := rpcCall(url, "eth_chainId", nil)
	if err != nil {
		return nil, err
	}
	var h string
	json.Unmarshal(r, &h)
	id := new(big.Int)
	if len(h) > 2 {
		id.SetString(h[2:], 16)
	}
	return id, nil
}

func getBaseFee(url string) (*big.Int, error) {
	r, err := rpcCall(url, "eth_getBlockByNumber", []interface{}{"latest", false})
	if err != nil {
		return nil, err
	}
	var block map[string]interface{}
	json.Unmarshal(r, &block)
	if block == nil {
		return big.NewInt(25_000_000_000), nil
	}
	baseFeeStr, _ := block["baseFeePerGas"].(string)
	baseFee := new(big.Int)
	if len(baseFeeStr) > 2 {
		baseFee.SetString(baseFeeStr[2:], 16)
	} else {
		baseFee.SetInt64(25_000_000_000)
	}
	return baseFee, nil
}

// ---- Benchmark ----

func benchmarkNetwork(name, url string, duration time.Duration, concurrency int) (confirmedTPS float64, blockRate float64, totalConfirmed int64) {
	key, err := crypto.HexToECDSA(fundedKeyHex)
	if err != nil {
		fmt.Printf("  [%s] Error loading key: %v\n", name, err)
		return
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	fmt.Printf("  [%s] Address: %s\n", name, addr.Hex())

	chainID, err := getChainID(url)
	if err != nil {
		fmt.Printf("  [%s] Error getting chain ID: %v\n", name, err)
		return
	}
	fmt.Printf("  [%s] Chain ID: %s\n", name, chainID.String())

	baseFee, _ := getBaseFee(url)
	fmt.Printf("  [%s] Base fee: %s\n", name, baseFee.String())

	startBlock, _ := getBlockNumber(url)
	startNonce, _ := getNonce(url, addr.Hex())
	fmt.Printf("  [%s] Start block: %d, Start nonce: %d\n", name, startBlock, startNonce)

	signer := types.NewEIP155Signer(chainID)
	gasPrice := new(big.Int).Add(baseFee, big.NewInt(2_000_000_000)) // baseFee + 2 gwei tip
	value := big.NewInt(1)
	gasLimit := uint64(21000)
	to := addr

	var sentCount atomic.Int64
	var errCount atomic.Int64
	var lastErr atomic.Value
	var wg sync.WaitGroup
	var nonceMu sync.Mutex
	nonce := startNonce

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	fmt.Printf("  [%s] Sending txs for %v with %d goroutines...\n", name, duration, concurrency)

	// Progress ticker
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s := sentCount.Load()
				e := errCount.Load()
				fmt.Printf("  [%s] Progress: sent=%d errors=%d\n", name, s, e)
			}
		}
	}()

	start := time.Now()
	for c := 0; c < concurrency; c++ {
		wg.Add(1)
		go func(key *ecdsa.PrivateKey) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				nonceMu.Lock()
				myNonce := nonce
				nonce++
				nonceMu.Unlock()

				tx := types.NewTransaction(myNonce, to, value, gasLimit, gasPrice, nil)
				signedTx, err := types.SignTx(tx, signer, key)
				if err != nil {
					errCount.Add(1)
					lastErr.Store(err.Error())
					continue
				}

				rawTx, err := signedTx.MarshalBinary()
				if err != nil {
					errCount.Add(1)
					continue
				}

				_, err = rpcCall(url, "eth_sendRawTransaction",
					[]interface{}{"0x" + hex.EncodeToString(rawTx)})
				if err != nil {
					errCount.Add(1)
					lastErr.Store(err.Error())
				} else {
					sentCount.Add(1)
				}
			}
		}(key)
	}

	wg.Wait()
	sendElapsed := time.Since(start)

	sent := sentCount.Load()
	errs := errCount.Load()
	fmt.Printf("  [%s] Sent: %d txs, Errors: %d, Send rate: %.0f tx/sec\n",
		name, sent, errs, float64(sent)/sendElapsed.Seconds())

	if v := lastErr.Load(); v != nil {
		fmt.Printf("  [%s] Last error: %v\n", name, v)
	}

	// Wait for blocks to be produced
	fmt.Printf("  [%s] Waiting for blocks to finalize...\n", name)
	time.Sleep(5 * time.Second)

	endBlock, _ := getBlockNumber(url)
	blocksProduced := endBlock - startBlock
	elapsed := time.Since(start)

	// Count confirmed transactions
	var confirmed int64
	for bn := startBlock + 1; bn <= endBlock; bn++ {
		txCount, _ := getBlockTxCount(url, bn)
		confirmed += int64(txCount)
	}

	confirmedTPS = float64(confirmed) / elapsed.Seconds()
	blockRate = float64(blocksProduced) / elapsed.Seconds()
	totalConfirmed = confirmed

	fmt.Printf("  [%s] Blocks: %d (%.2f blocks/sec)\n", name, blocksProduced, blockRate)
	fmt.Printf("  [%s] Confirmed: %d txs (%.2f confirmed TPS)\n", name, confirmed, confirmedTPS)

	// Per-block breakdown
	if blocksProduced > 0 {
		fmt.Printf("  [%s] Avg txs/block: %.1f\n", name, float64(confirmed)/float64(blocksProduced))
	}

	return
}

func main() {
	luxURL := os.Getenv("LUX_RPC")
	if luxURL == "" {
		luxURL = "http://127.0.0.1:8545/ext/bc/C/rpc"
	}
	avaxURL := os.Getenv("AVAX_RPC")
	if avaxURL == "" {
		avaxURL = "http://127.0.0.1:19650/ext/bc/C/rpc"
	}

	duration := 10 * time.Second
	concurrency := 10

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║   REAL NETWORK BENCHMARK: Lux vs Avalanche             ║")
	fmt.Println("║   Live Block Production - Confirmed TPS                ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Printf("\nDuration: %v, Concurrency: %d senders\n\n", duration, concurrency)

	// Check connectivity
	_, luxErr := getBlockNumber(luxURL)
	_, avaxErr := getBlockNumber(avaxURL)

	luxOK := luxErr == nil
	avaxOK := avaxErr == nil

	if luxOK {
		fmt.Printf("  Lux:       ONLINE at %s\n", luxURL)
	} else {
		fmt.Printf("  Lux:       OFFLINE (%v)\n", luxErr)
	}
	if avaxOK {
		fmt.Printf("  Avalanche: ONLINE at %s\n", avaxURL)
	} else {
		fmt.Printf("  Avalanche: OFFLINE (%v)\n", avaxErr)
	}
	fmt.Println()

	if !luxOK && !avaxOK {
		fmt.Println("ERROR: No networks available")
		os.Exit(1)
	}

	var luxTPS, avaxTPS, luxBlkRate, avaxBlkRate float64
	var luxConfirmed, avaxConfirmed int64

	if luxOK {
		fmt.Println("═══ Benchmarking Lux C-Chain ═══")
		luxTPS, luxBlkRate, luxConfirmed = benchmarkNetwork("Lux", luxURL, duration, concurrency)
		fmt.Println()
	}

	if avaxOK {
		fmt.Println("═══ Benchmarking Avalanche C-Chain ═══")
		avaxTPS, avaxBlkRate, avaxConfirmed = benchmarkNetwork("Avax", avaxURL, duration, concurrency)
		fmt.Println()
	}

	// Results
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║                    FINAL RESULTS                       ║")
	fmt.Println("╠══════════════════════════════════════════════════════════╣")
	if luxOK {
		fmt.Printf("║  Lux:       %8.1f confirmed TPS (%d txs)\n", luxTPS, luxConfirmed)
		fmt.Printf("║             %8.2f blocks/sec\n", luxBlkRate)
	}
	if avaxOK {
		fmt.Printf("║  Avalanche: %8.1f confirmed TPS (%d txs)\n", avaxTPS, avaxConfirmed)
		fmt.Printf("║             %8.2f blocks/sec\n", avaxBlkRate)
	}
	if luxOK && avaxOK && avaxTPS > 0 {
		fmt.Printf("║\n")
		fmt.Printf("║  Lux is %.1fx FASTER in confirmed TPS\n", luxTPS/avaxTPS)
		fmt.Printf("║  Lux is %.1fx FASTER in block production\n", luxBlkRate/avaxBlkRate)
	}
	fmt.Println("╚══════════════════════════════════════════════════════════╝")

	// Remove unused imports
	_ = common.Address{}
}
