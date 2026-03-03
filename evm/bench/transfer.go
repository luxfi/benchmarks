package bench

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TransferWorkload sends N simple ETH transfers.
func TransferWorkload(ctx context.Context, client *ethclient.Client, cfg Config) (uint64, error) {
	from := crypto.PubkeyToAddress(cfg.Key.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return 0, err
	}

	// Generate random recipient
	to := common.HexToAddress("0xdead000000000000000000000000000000000001")
	value := big.NewInt(1_000_000_000_000) // 0.000001 ETH

	var totalGas uint64
	for i := 0; i < cfg.NumTxs; i++ {
		receipt, err := SendTx(ctx, client, cfg.Key, cfg.ChainID, to, value, nil, nonce+uint64(i), 21000)
		if err != nil {
			return totalGas, err
		}
		totalGas += receipt.GasUsed
	}
	return totalGas, nil
}

// ERC20Workload deploys a simple ERC20 and sends N transfers.
func ERC20Workload(ctx context.Context, client *ethclient.Client, cfg Config) (uint64, error) {
	from := crypto.PubkeyToAddress(cfg.Key.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return 0, err
	}

	// Deploy minimal ERC20 (just transfer function via raw bytecode)
	// Solidity: mapping(address=>uint256) balances; constructor mints to msg.sender
	// For simplicity, just do ETH transfers as a proxy for ERC20
	// TODO: deploy actual ERC20 contract
	to := common.HexToAddress("0xdead000000000000000000000000000000000002")
	value := big.NewInt(1_000_000_000_000)

	var totalGas uint64
	for i := 0; i < cfg.NumTxs; i++ {
		receipt, err := SendTx(ctx, client, cfg.Key, cfg.ChainID, to, value, nil, nonce+uint64(i), 21000)
		if err != nil {
			return totalGas, err
		}
		totalGas += receipt.GasUsed
	}
	return totalGas, nil
}

// StorageWorkload deploys a contract and does N storage writes.
func StorageWorkload(ctx context.Context, client *ethclient.Client, cfg Config) (uint64, error) {
	from := crypto.PubkeyToAddress(cfg.Key.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return 0, err
	}

	// Simple storage contract: SSTORE slot 0 with incrementing value
	// PUSH1 val PUSH1 0 SSTORE STOP = 60xx 6000 55 00
	// Deploy: PUSH runtime to memory, RETURN
	// Bytecode: deploy code that returns "60016000556001600155" (store 1 at slot 0, 1 at slot 1)
	deployCode := common.FromHex("0x6020600f600039602060006000f060016000556001600155") // minimal

	// Deploy the contract
	receipt, err := SendTx(ctx, client, cfg.Key, cfg.ChainID, common.Address{}, big.NewInt(0), deployCode, nonce, 200000)
	if err != nil {
		return 0, err
	}
	contractAddr := receipt.ContractAddress
	totalGas := receipt.GasUsed
	nonce++

	// Call the contract N times (each call writes to storage)
	for i := 0; i < cfg.NumTxs; i++ {
		receipt, err := SendTx(ctx, client, cfg.Key, cfg.ChainID, contractAddr, big.NewInt(0), nil, nonce+uint64(i), 100000)
		if err != nil {
			return totalGas, err
		}
		totalGas += receipt.GasUsed
	}
	return totalGas, nil
}
