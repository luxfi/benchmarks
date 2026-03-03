package bench

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func workloadKey() *ecdsa.PrivateKey    { return DefaultKey() }
func workloadChainID() *big.Int         { return params.MergedTestChainConfig.ChainID }

func signTx(tx *types.Transaction, key *ecdsa.PrivateKey, chainID *big.Int) (*types.Transaction, error) {
	return types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
}

// GenerateTransfers creates N simple ETH transfer transactions (21000 gas each).
func GenerateTransfers(n int) ([]*types.Transaction, error) {
	key := workloadKey()
	chainID := workloadChainID()
	to := common.HexToAddress("0xdead000000000000000000000000000000000001")

	txs := make([]*types.Transaction, n)
	for i := 0; i < n; i++ {
		tx := types.NewTx(&types.LegacyTx{
			Nonce: uint64(i), To: &to, Value: big.NewInt(1),
			Gas: 21000, GasPrice: big.NewInt(0),
		})
		signed, err := signTx(tx, key, chainID)
		if err != nil {
			return nil, fmt.Errorf("sign transfer %d: %w", i, err)
		}
		txs[i] = signed
	}
	return txs, nil
}

// GenerateERC20Transfers deploys a minimal token contract + N transfer calls.
// The contract does 2 SLOADs + 2 SSTOREs per call, matching the gas profile
// of a real ERC20 transfer (~31k gas per call).
//
// Runtime: load slot 0, decrement, store slot 0, load slot 1, increment, store slot 1.
// Constructor: store MAX_UINT256 at slot 0 (initial balance).
func GenerateERC20Transfers(n int) ([]*types.Transaction, error) {
	key := workloadKey()
	chainID := workloadChainID()
	sender := crypto.PubkeyToAddress(key.PublicKey)

	// Runtime: 2 SLOADs + 2 SSTOREs per call
	// PUSH1 0 SLOAD | PUSH1 1 SWAP1 SUB | PUSH1 0 SSTORE |
	// PUSH1 1 SLOAD | PUSH1 1 ADD | PUSH1 1 SSTORE | STOP
	runtime := common.FromHex("6000546001900360005560015460010160015500")

	initLogic := common.FromHex(
		"7f" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" +
			"6000" + "55") // PUSH32 max | PUSH1 0 | SSTORE

	deployCode := makeDeployCode(runtime, initLogic)

	nonce := uint64(0)
	deployTx, err := makeSignedTx(nonce, nil, deployCode, 500_000, key, chainID)
	if err != nil {
		return nil, err
	}
	contractAddr := crypto.CreateAddress(sender, nonce)
	nonce++

	txs := make([]*types.Transaction, 0, n+1)
	txs = append(txs, deployTx)

	for i := 0; i < n; i++ {
		tx, err := makeSignedTx(nonce, &contractAddr, nil, 100_000, key, chainID)
		if err != nil {
			return nil, fmt.Errorf("sign erc20 call %d: %w", i, err)
		}
		txs = append(txs, tx)
		nonce++
	}
	return txs, nil
}

// GenerateStorageWrites deploys a contract + N calls that each write to a new storage slot.
// Runtime: increment counter at slot 0, store 1 at slot[counter].
// Gives cold SSTORE per call (~48k gas).
func GenerateStorageWrites(n int) ([]*types.Transaction, error) {
	key := workloadKey()
	chainID := workloadChainID()
	sender := crypto.PubkeyToAddress(key.PublicKey)

	// PUSH1 0 SLOAD | PUSH1 1 ADD | DUP1 PUSH1 0 SSTORE | PUSH1 1 SWAP1 SSTORE | STOP
	runtime := common.FromHex("600054600101806000556001905500")
	deployCode := makeSimpleDeployCode(runtime)

	nonce := uint64(0)
	deployTx, err := makeSignedTx(nonce, nil, deployCode, 200_000, key, chainID)
	if err != nil {
		return nil, err
	}
	contractAddr := crypto.CreateAddress(sender, nonce)
	nonce++

	txs := make([]*types.Transaction, 0, n+1)
	txs = append(txs, deployTx)

	for i := 0; i < n; i++ {
		tx, err := makeSignedTx(nonce, &contractAddr, nil, 100_000, key, chainID)
		if err != nil {
			return nil, fmt.Errorf("sign storage write %d: %w", i, err)
		}
		txs = append(txs, tx)
		nonce++
	}
	return txs, nil
}

// GenerateUniswapSwaps deploys a constant-product AMM contract + N swap calls.
// Storage: slot 0=reserve0, slot 1=reserve1, slot 2=k (invariant).
// Each swap: 3 SLOADs + arithmetic (ADD, DIV, MUL) + 3 SSTOREs (~36k gas).
func GenerateUniswapSwaps(n int) ([]*types.Transaction, error) {
	key := workloadKey()
	chainID := workloadChainID()
	sender := crypto.PubkeyToAddress(key.PublicKey)

	// Runtime: load r0,r1,k | newR0=r0+1e15 | newR1=k/newR0 | store both | newK=newR0*newR1 | store k
	runtime := common.FromHex(
		"600054" + // SLOAD slot 0 -> r0
			"600154" + // SLOAD slot 1 -> r1
			"600254" + // SLOAD slot 2 -> k; stack: [k, r1, r0]
			"91" + // SWAP2 -> [r0, r1, k]
			"66038D7EA4C68000" + // PUSH7 1e15 (amountIn)
			"01" + // ADD -> [newR0, r1, k]
			"91" + // SWAP2 -> [k, r1, newR0]
			"82" + // DUP3 -> [newR0, k, r1, newR0]
			"90" + // SWAP1 -> [k, newR0, r1, newR0]
			"04" + // DIV -> [newR1, r1, newR0]
			"9050" + // SWAP1 POP -> [newR1, newR0]
			"81" + // DUP2 -> [newR0, newR1, newR0]
			"600055" + // SSTORE(0, newR0) -> [newR1, newR0]
			"80" + // DUP1 -> [newR1, newR1, newR0]
			"600155" + // SSTORE(1, newR1) -> [newR1, newR0]
			"02" + // MUL -> [newK]
			"600255" + // SSTORE(2, newK)
			"00") // STOP

	// Constructor: init reserves to 1e18 each, k = 1e36
	initLogic := common.FromHex(
		"670DE0B6B3A7640000" + // PUSH8 1e18
			"80808060005560015502600255") // DUP DUP DUP | SSTORE(0,1e18) | SSTORE(1,1e18) | MUL | SSTORE(2,1e36)

	deployCode := makeDeployCode(runtime, initLogic)

	nonce := uint64(0)
	deployTx, err := makeSignedTx(nonce, nil, deployCode, 500_000, key, chainID)
	if err != nil {
		return nil, err
	}
	contractAddr := crypto.CreateAddress(sender, nonce)
	nonce++

	txs := make([]*types.Transaction, 0, n+1)
	txs = append(txs, deployTx)

	for i := 0; i < n; i++ {
		tx, err := makeSignedTx(nonce, &contractAddr, nil, 200_000, key, chainID)
		if err != nil {
			return nil, fmt.Errorf("sign swap %d: %w", i, err)
		}
		txs = append(txs, tx)
		nonce++
	}
	return txs, nil
}

// GenerateMixedDeFi creates a realistic mix: 40% transfers, 30% ERC20, 20% storage, 10% swaps.
// Deploys all contracts first, then interleaves workload transactions.
func GenerateMixedDeFi(n int) ([]*types.Transaction, error) {
	key := workloadKey()
	chainID := workloadChainID()
	sender := crypto.PubkeyToAddress(key.PublicKey)

	nTransfers := n * 40 / 100
	nERC20 := n * 30 / 100
	nStorage := n * 20 / 100
	nSwaps := n - nTransfers - nERC20 - nStorage

	nonce := uint64(0)

	// Deploy ERC20
	erc20Runtime := common.FromHex("6000546001900360005560015460010160015500")
	erc20Init := common.FromHex("7f" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" + "6000" + "55")
	erc20Deploy, err := makeSignedTx(nonce, nil, makeDeployCode(erc20Runtime, erc20Init), 500_000, key, chainID)
	if err != nil {
		return nil, err
	}
	erc20Addr := crypto.CreateAddress(sender, nonce)
	nonce++

	// Deploy storage
	storageRuntime := common.FromHex("600054600101806000556001905500")
	storageDeploy, err := makeSignedTx(nonce, nil, makeSimpleDeployCode(storageRuntime), 200_000, key, chainID)
	if err != nil {
		return nil, err
	}
	storageAddr := crypto.CreateAddress(sender, nonce)
	nonce++

	// Deploy AMM
	ammRuntime := common.FromHex(
		"600054600154600254" +
			"9166038D7EA4C6800001" +
			"918290049050" +
			"81600055806001550260025500")
	ammInit := common.FromHex("670DE0B6B3A764000080808060005560015502600255")
	ammDeploy, err := makeSignedTx(nonce, nil, makeDeployCode(ammRuntime, ammInit), 500_000, key, chainID)
	if err != nil {
		return nil, err
	}
	ammAddr := crypto.CreateAddress(sender, nonce)
	nonce++

	txs := make([]*types.Transaction, 0, n+3)
	txs = append(txs, erc20Deploy, storageDeploy, ammDeploy)

	to := common.HexToAddress("0xdead000000000000000000000000000000000001")

	for i := 0; i < nTransfers; i++ {
		tx := types.NewTx(&types.LegacyTx{
			Nonce: nonce, To: &to, Value: big.NewInt(1),
			Gas: 21000, GasPrice: big.NewInt(0),
		})
		signed, err := signTx(tx, key, chainID)
		if err != nil {
			return nil, err
		}
		txs = append(txs, signed)
		nonce++
	}
	for i := 0; i < nERC20; i++ {
		tx, err := makeSignedTx(nonce, &erc20Addr, nil, 100_000, key, chainID)
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
		nonce++
	}
	for i := 0; i < nStorage; i++ {
		tx, err := makeSignedTx(nonce, &storageAddr, nil, 100_000, key, chainID)
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
		nonce++
	}
	for i := 0; i < nSwaps; i++ {
		tx, err := makeSignedTx(nonce, &ammAddr, nil, 200_000, key, chainID)
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
		nonce++
	}

	return txs, nil
}

// makeSignedTx creates and signs a legacy transaction.
func makeSignedTx(nonce uint64, to *common.Address, data []byte, gas uint64, key *ecdsa.PrivateKey, chainID *big.Int) (*types.Transaction, error) {
	value := big.NewInt(0)
	if to != nil && len(data) == 0 {
		value = big.NewInt(1)
	}
	tx := types.NewTx(&types.LegacyTx{
		Nonce: nonce, To: to, Value: value,
		Gas: gas, GasPrice: big.NewInt(0), Data: data,
	})
	return signTx(tx, key, chainID)
}

// makeSimpleDeployCode returns init code that deploys the given runtime bytecode.
func makeSimpleDeployCode(runtime []byte) []byte {
	return makeDeployCode(runtime, nil)
}

// makeDeployCode returns init code: optional initLogic + CODECOPY/RETURN of runtime.
func makeDeployCode(runtime []byte, initLogic []byte) []byte {
	rLen := len(runtime)
	codecopyBytes := 12 // PUSH1 + PUSH1 + PUSH1 + CODECOPY(1) + PUSH1 + PUSH1 + RETURN(1)
	offset := len(initLogic) + codecopyBytes

	suffix := []byte{
		0x60, byte(rLen),    // PUSH1 runtimeLen
		0x60, byte(offset),  // PUSH1 codecopyOffset
		0x60, 0x00,          // PUSH1 0
		0x39,                // CODECOPY
		0x60, byte(rLen),    // PUSH1 runtimeLen
		0x60, 0x00,          // PUSH1 0
		0xf3,                // RETURN
	}

	code := make([]byte, 0, offset+rLen)
	code = append(code, initLogic...)
	code = append(code, suffix...)
	code = append(code, runtime...)
	return code
}
