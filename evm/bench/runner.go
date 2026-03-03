// Package bench provides EVM benchmark workloads.
package bench

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Result holds benchmark results for one workload on one EVM.
type Result struct {
	EVM       string
	Workload  string
	TxCount   int
	Duration  time.Duration
	TPS       float64
	GasUsed   uint64
	MgasPerS  float64
	AvgLatMs  float64
}

// Config for a benchmark run.
type Config struct {
	RPC      string
	Label    string
	ChainID  *big.Int
	Key      *ecdsa.PrivateKey
	NumTxs   int
}

// DefaultKey returns the LIGHT_MNEMONIC index 0 private key.
// m/44'/60'/0'/0/0 from "light" x 11 + "energy" via luxfi/go-bip32.
// Address: 0x35D64Ff3f618f7a17DF34DCb21be375A4686a8de
// This is a well-known dev key — DO NOT use on mainnet.
func DefaultKey() *ecdsa.PrivateKey {
	key, _ := crypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	return key
}

// Run executes a workload and returns results.
func Run(ctx context.Context, cfg Config, workload func(ctx context.Context, client *ethclient.Client, cfg Config) (gasUsed uint64, err error)) (*Result, error) {
	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.RPC, err)
	}
	defer client.Close()

	start := time.Now()
	gasUsed, err := workload(ctx, client, cfg)
	elapsed := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("%s workload failed: %w", cfg.Label, err)
	}

	tps := float64(cfg.NumTxs) / elapsed.Seconds()
	mgasPerS := float64(gasUsed) / elapsed.Seconds() / 1e6

	return &Result{
		EVM:      cfg.Label,
		TxCount:  cfg.NumTxs,
		Duration: elapsed,
		TPS:      tps,
		GasUsed:  gasUsed,
		MgasPerS: mgasPerS,
		AvgLatMs: float64(elapsed.Milliseconds()) / float64(cfg.NumTxs),
	}, nil
}

// SendTx signs and sends a transaction, waits for receipt.
func SendTx(ctx context.Context, client *ethclient.Client, key *ecdsa.PrivateKey, chainID *big.Int, to common.Address, value *big.Int, data []byte, nonce uint64, gasLimit uint64) (*types.Receipt, error) {
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		gasPrice = big.NewInt(25_000_000_000) // 25 gwei fallback
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    value,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	})

	signer := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	// Wait for receipt (poll)
	for i := 0; i < 100; i++ {
		receipt, err := client.TransactionReceipt(ctx, signed.Hash())
		if err == nil {
			return receipt, nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	return nil, fmt.Errorf("tx %s not mined after 5s", signed.Hash().Hex())
}

// PrintReport prints a comparison table.
func PrintReport(results []*Result) {
	// Group by workload
	workloads := make(map[string][]*Result)
	var order []string
	for _, r := range results {
		if _, ok := workloads[r.Workload]; !ok {
			order = append(order, r.Workload)
		}
		workloads[r.Workload] = append(workloads[r.Workload], r)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    EVM Benchmark Report                              ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ %-18s│ %-12s│ %-12s│ %-12s│ %-8s║\n", "Workload", "TPS", "Mgas/s", "Avg Lat", "EVM")
	fmt.Println("║──────────────────┼─────────────┼─────────────┼─────────────┼─────────║")

	for _, wl := range order {
		for _, r := range workloads[wl] {
			fmt.Printf("║ %-18s│ %10.1f  │ %10.2f  │ %8.1fms  │ %-8s║\n",
				truncate(r.Workload, 18), r.TPS, r.MgasPerS, r.AvgLatMs, truncate(r.EVM, 8))
		}
	}
	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s + strings.Repeat(" ", n-len(s))
	}
	return s[:n-1] + "…"
}
