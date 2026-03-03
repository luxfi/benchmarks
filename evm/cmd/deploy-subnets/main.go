// deploy-subnets creates 2 subnet chains on a running Lux localnet for EVM benchmarking.
//
// Usage:
//
//	go run ./cmd/deploy-subnets
//	go run ./cmd/deploy-subnets --uri=http://127.0.0.1:9660
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/luxfi/crypto/secp256k1"
	"github.com/luxfi/ids"
	"github.com/luxfi/math/set"
	"github.com/luxfi/node/wallet/network/primary"
	"github.com/luxfi/utxo/secp256k1fx"

	"github.com/luxfi/go-bip32"
	"github.com/luxfi/go-bip39"
)

// VM IDs for the two EVM implementations under test.
var (
	evmgpuVMID = ids.FromStringOrPanic("Bi6QsLSx5epinXk4eHEBsnNVUW4ZhVmR7L6frnBhY6yCMcpMW")
	revmVMID   = ids.FromStringOrPanic("2CwRhy6P6fQyn3JtZW68BFvn2nRwZ4Z8iar8EEeeLpGKmn5XRT")
)

// LIGHT_MNEMONIC: "light" x 11 + "energy"
var lightMnemonic = strings.TrimSpace(
	"light light light light light light light light light light light energy",
)

type chainDef struct {
	Name    string
	VMID    ids.ID
	ChainID int
}

func main() {
	uri := flag.String("uri", "http://127.0.0.1:9660", "Lux node P-Chain RPC URI")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Derive key from LIGHT_MNEMONIC at m/44'/9000'/0'/0/0 (Lux P/X-Chain coin type).
	privKey := deriveKey(lightMnemonic, 9000, 0)
	addr := privKey.Address()
	log.Printf("Key address: %s", addr)

	kc := secp256k1fx.NewKeychain(privKey)
	adapter := primary.NewKeychainAdapter(kc)

	chains := []chainDef{
		{Name: "evmgpu", VMID: evmgpuVMID, ChainID: 200200},
		{Name: "revm", VMID: revmVMID, ChainID: 200300},
	}

	type result struct {
		Name         string
		SubnetID     ids.ID
		BlockchainID ids.ID
	}
	var results []result

	for _, ch := range chains {
		log.Printf("=== Deploying %s (chainId %d, VM %s) ===", ch.Name, ch.ChainID, ch.VMID)

		// Sync wallet.
		wallet, err := primary.MakeWallet(ctx, &primary.WalletConfig{
			URI:         *uri,
			LUXKeychain: adapter,
			EthKeychain: adapter,
		})
		if err != nil {
			log.Fatalf("wallet sync for %s: %v", ch.Name, err)
		}

		// Create subnet.
		log.Printf("Creating subnet for %s...", ch.Name)
		owner := &secp256k1fx.OutputOwners{
			Threshold: 1,
			Addrs:     []ids.ShortID{addr},
		}
		subnetTx, err := wallet.P().IssueCreateNetworkTx(owner)
		if err != nil {
			log.Fatalf("create subnet for %s: %v", ch.Name, err)
		}
		subnetID := subnetTx.ID()
		log.Printf("Subnet: %s", subnetID)

		// Re-sync wallet with the subnet tx so ownership is cached.
		wallet2, err := primary.MakeWallet(ctx, &primary.WalletConfig{
			URI:              *uri,
			LUXKeychain:      adapter,
			EthKeychain:      adapter,
			PChainTxsToFetch: set.Of(subnetID),
		})
		if err != nil {
			log.Fatalf("wallet re-sync for %s: %v", ch.Name, err)
		}

		// Build genesis.
		genesis := makeGenesis(ch.ChainID)

		// Create chain.
		log.Printf("Creating chain %s on subnet %s...", ch.Name, subnetID)
		chainTx, err := wallet2.P().IssueCreateChainTx(
			subnetID,
			genesis,
			ch.VMID,
			nil,
			ch.Name,
		)
		if err != nil {
			log.Fatalf("create chain %s: %v", ch.Name, err)
		}
		blockchainID := chainTx.ID()
		log.Printf("Blockchain: %s", blockchainID)

		results = append(results, result{
			Name:         ch.Name,
			SubnetID:     subnetID,
			BlockchainID: blockchainID,
		})
	}

	// Print results.
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════")
	fmt.Println("  EVM Bench — Subnet Chains Deployed")
	fmt.Println("════════════════════════════════════════════════════")
	for _, r := range results {
		fmt.Printf("  %s\n", r.Name)
		fmt.Printf("    Subnet:     %s\n", r.SubnetID)
		fmt.Printf("    Blockchain: %s\n", r.BlockchainID)
		fmt.Printf("    RPC:        %s/ext/bc/%s/rpc\n", *uri, r.BlockchainID)
		fmt.Printf("    WS:         %s/ext/bc/%s/ws\n", *uri, r.BlockchainID)
		fmt.Println()
	}
	fmt.Println("════════════════════════════════════════════════════")
}

// makeGenesis returns a minimal EVM genesis JSON.
func makeGenesis(chainID int) []byte {
	genesis := map[string]interface{}{
		"config": map[string]interface{}{
			"chainId":             chainID,
			"homesteadBlock":      0,
			"eip150Block":         0,
			"eip155Block":         0,
			"eip158Block":         0,
			"byzantiumBlock":      0,
			"constantinopleBlock": 0,
			"petersburgBlock":     0,
			"istanbulBlock":       0,
			"muirGlacierBlock":    0,
			"subnetEVMTimestamp":  0,
		},
		"gasLimit":   "0x5F5E100", // 100_000_000
		"difficulty": "0x0",
		"alloc": map[string]interface{}{
			// Funded dev account: 0x9011E888251AB053B7bD1cdB598Db4f9DEd94714
			"9011E888251AB053B7bD1cdB598Db4f9DEd94714": map[string]string{
				"balance": "0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF",
			},
		},
	}
	b, err := json.Marshal(genesis)
	if err != nil {
		log.Fatalf("marshal genesis: %v", err)
	}
	return b
}

// deriveKey derives a secp256k1 private key from a BIP39 mnemonic at
// m/44'/{coinType}'/0'/0/{index}.
func deriveKey(mnemonic string, coinType, index uint32) *secp256k1.PrivateKey {
	seed := bip39.NewSeed(mnemonic, "")
	defer func() {
		for i := range seed {
			seed[i] = 0
		}
	}()

	master, err := bip32.NewMasterKey(seed)
	if err != nil {
		log.Fatalf("bip32 master key: %v", err)
	}

	// m/44'/{coinType}'/0'/0/{index}
	path := []uint32{
		bip32.FirstHardenedChild + 44,
		bip32.FirstHardenedChild + coinType,
		bip32.FirstHardenedChild + 0,
		0,
		index,
	}
	k := master
	for _, p := range path {
		k, err = k.NewChildKey(p)
		if err != nil {
			log.Fatalf("bip32 derive: %v", err)
		}
	}

	// Normalize to 32 bytes (left-pad with zeros if needed).
	var keyBytes [32]byte
	copy(keyBytes[32-len(k.Key):], k.Key)

	privKey, err := secp256k1.ToPrivateKey(keyBytes[:])
	if err != nil {
		log.Fatalf("invalid derived key: %v", err)
	}
	return privKey
}
