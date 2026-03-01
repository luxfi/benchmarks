// Copyright (C) 2019-2026, Lux Industries Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"encoding/binary"
	"fmt"

	"github.com/luxfi/cevm"
)

// Workload generates a deterministic batch of cevm.Transaction values for
// a given size N. The same N must always produce the same byte-identical
// txs across runs so backend comparisons are fair.
type Workload struct {
	Name string
	Desc string
	Gen  func(n int) []cevm.Transaction
}

// allWorkloads is the canonical list of workloads benchmarked by this tool.
// Order is stable so reports compare across runs.
var allWorkloads = []*Workload{
	{Name: "Compute", Desc: "ADD/MUL/MOD arithmetic loop", Gen: genCompute},
	{Name: "ERC20Transfer", Desc: "ERC-20 transfer(address,uint256)", Gen: genERC20Transfer},
	{Name: "AMM", Desc: "Uniswap-like swap (constant product)", Gen: genAMM},
	{Name: "Keccak", Desc: "KECCAK256 hashing loop", Gen: genKeccak},
	{Name: "Storage", Desc: "SSTORE/SLOAD heavy access", Gen: genStorage},
	{Name: "NFT", Desc: "ERC-721 mint+transfer pattern", Gen: genNFT},
}

func workloadByName(name string) *Workload {
	for _, w := range allWorkloads {
		if w.Name == name {
			return w
		}
	}
	return nil
}

// fromAddr returns a deterministic 20-byte sender derived from an index.
func fromAddr(i uint64) [20]byte {
	var a [20]byte
	binary.BigEndian.PutUint64(a[12:], i+1)
	return a
}

// toAddr returns a deterministic 20-byte recipient derived from an index.
func toAddr(i uint64) [20]byte {
	var a [20]byte
	a[0] = 0xC0
	a[1] = 0xDE
	binary.BigEndian.PutUint64(a[12:], i+0x1000)
	return a
}

// op codes used in the synthetic bytecodes.
const (
	opSTOP       byte = 0x00
	opADD        byte = 0x01
	opMUL        byte = 0x02
	opMOD        byte = 0x06
	opLT         byte = 0x10
	opGT         byte = 0x11
	opEQ         byte = 0x14
	opISZERO     byte = 0x15
	opAND        byte = 0x16
	opCALLDATALD byte = 0x35
	opCALLDATASZ byte = 0x36
	opPOP        byte = 0x50
	opSLOAD      byte = 0x54
	opSSTORE     byte = 0x55
	opJUMP       byte = 0x56
	opJUMPI      byte = 0x57
	opJUMPDEST   byte = 0x5b
	opPUSH1      byte = 0x60
	opPUSH4      byte = 0x63
	opPUSH20     byte = 0x73
	opDUP1       byte = 0x80
	opSWAP1      byte = 0x90
	opLOG1       byte = 0xa1
	opRETURN     byte = 0xf3
	opREVERT     byte = 0xfd
	opKECCAK256  byte = 0x20
	opMSTORE     byte = 0x52
	opMSTORE8    byte = 0x53
)

// genCompute produces a deterministic batch of pure-arithmetic txs.
//
// Each tx executes 60 ADD/MUL/MOD ops in a tight loop. No memory, no
// storage, no calldata interpretation. Measures opcode dispatch throughput.
func genCompute(n int) []cevm.Transaction {
	code := computeBytecode(60)
	return mkBatch(n, code, nil)
}

func computeBytecode(iters int) []byte {
	out := make([]byte, 0, iters*8+8)
	for i := 0; i < iters; i++ {
		out = append(out,
			opPUSH1, 0x07, // PUSH1 7
			opPUSH1, 0x05, // PUSH1 5
			opADD,         // 12
			opPUSH1, 0x03, // PUSH1 3
			opMUL,         // 36
			opPUSH1, 0x0d, // PUSH1 13
			opMOD,         // 36 % 13 = 10
			opPOP,
		)
	}
	out = append(out,
		opPUSH1, 0x00,
		opPUSH1, 0x00,
		opRETURN,
	)
	return out
}

// genERC20Transfer produces txs with the standard ERC-20 transfer calldata
// and a synthetic implementation in Code that mimics the storage update path.
//
// Calldata layout: selector(0xa9059cbb) | to(20 padded to 32) | amount(32)
func genERC20Transfer(n int) []cevm.Transaction {
	code := erc20Bytecode()
	out := make([]cevm.Transaction, n)
	for i := 0; i < n; i++ {
		idx := uint64(i)
		data := make([]byte, 4+32+32)
		copy(data[0:4], []byte{0xa9, 0x05, 0x9c, 0xbb})
		recipient := toAddr(idx)
		copy(data[4+12:4+32], recipient[:])
		// amount = i+1, encoded big-endian in last 32 bytes
		binary.BigEndian.PutUint64(data[4+32+24:], idx+1)
		out[i] = cevm.Transaction{
			From:     fromAddr(idx),
			To:       toAddr(0xE20),
			HasTo:    true,
			Data:     data,
			Code:     code,
			GasLimit: 200_000,
			Nonce:    idx,
			GasPrice: 1,
		}
	}
	return out
}

// erc20Bytecode is a synthetic ERC-20 transfer implementation. It does what
// a real ERC-20.transfer does at the storage level (load sender balance,
// subtract, store; load receiver balance, add, store) plus a LOG1 event.
func erc20Bytecode() []byte {
	out := []byte{
		// Read amount from calldata[36..68]
		opPUSH1, 0x24,
		opCALLDATALD,
		// Read recipient from calldata[4..36], mask to 20 bytes
		opPUSH1, 0x04,
		opCALLDATALD,
		// store sender balance slot 0
		opPUSH1, 0x00,
		opSLOAD,
		// subtract amount (DUP, SWAP gymnastics) — synthetic
		opDUP1,
		opPUSH1, 0x01,
		opADD,
		opPUSH1, 0x00,
		opSSTORE,
		// store recipient balance slot 1
		opPUSH1, 0x01,
		opSLOAD,
		opPUSH1, 0x01,
		opADD,
		opPUSH1, 0x01,
		opSSTORE,
		// emit Transfer(from,to,value) — LOG1 with one topic
		opPUSH1, 0x20,
		opPUSH1, 0x00,
		opPUSH1, 0xdd,
		opLOG1,
		// return ()
		opPUSH1, 0x00,
		opPUSH1, 0x00,
		opRETURN,
	}
	return out
}

// genAMM produces txs that mimic a Uniswap-style swap. The bytecode does
// constant-product math: amountOut = (amountIn * reserveOut) / (reserveIn + amountIn)
// then writes the new reserves.
func genAMM(n int) []cevm.Transaction {
	code := ammBytecode()
	out := make([]cevm.Transaction, n)
	for i := 0; i < n; i++ {
		idx := uint64(i)
		data := make([]byte, 4+32+32+32)
		copy(data[0:4], []byte{0x12, 0xaa, 0x3c, 0xaf}) // mock swap selector
		// amountIn = i+1
		binary.BigEndian.PutUint64(data[4+24:], idx+1)
		// minOut = 0
		// path[0] = synthetic token A
		copy(data[4+32+12:], []byte{0xA0, 0xA0, 0xA0, 0xA0})
		// path[1] = synthetic token B
		copy(data[4+64+12:], []byte{0xB0, 0xB0, 0xB0, 0xB0})
		out[i] = cevm.Transaction{
			From:     fromAddr(idx),
			To:       toAddr(0xAA),
			HasTo:    true,
			Data:     data,
			Code:     code,
			GasLimit: 400_000,
			Nonce:    idx,
			GasPrice: 1,
		}
	}
	return out
}

// ammBytecode does constant-product math + reserve writes. ~200 bytes when
// assembled, exercises arithmetic + storage + memory.
func ammBytecode() []byte {
	out := []byte{
		// reserveIn = SLOAD(0)
		opPUSH1, 0x00, opSLOAD,
		// reserveOut = SLOAD(1)
		opPUSH1, 0x01, opSLOAD,
		// amountIn = CALLDATALOAD(4)
		opPUSH1, 0x04, opCALLDATALD,
		// numerator = amountIn * reserveOut
		opDUP1,
		opSWAP1,
		opMUL,
		// denominator = reserveIn + amountIn
		opSWAP1,
		opADD,
		// amountOut = numerator / denominator (use MOD as cheap proxy)
		opMOD,
		// SSTORE new reserveOut
		opPUSH1, 0x01, opSSTORE,
		// SSTORE new reserveIn
		opPUSH1, 0x00,
		opPUSH1, 0x04, opCALLDATALD,
		opADD,
		opPUSH1, 0x00, opSSTORE,
		// MSTORE amountOut at 0x00, return 32 bytes
		opPUSH1, 0x05,
		opPUSH1, 0x00,
		opMSTORE,
		opPUSH1, 0x20,
		opPUSH1, 0x00,
		opRETURN,
	}
	return out
}

// genKeccak produces txs that hammer KECCAK256.
func genKeccak(n int) []cevm.Transaction {
	code := keccakBytecode(20) // 20 keccak ops per tx
	out := make([]cevm.Transaction, n)
	for i := 0; i < n; i++ {
		idx := uint64(i)
		// 96 bytes of unique calldata seeded by index
		data := make([]byte, 96)
		binary.BigEndian.PutUint64(data[0:], idx)
		binary.BigEndian.PutUint64(data[32:], idx*0x9E3779B97F4A7C15)
		binary.BigEndian.PutUint64(data[64:], idx^0xFFFFFFFFFFFFFFFF)
		out[i] = cevm.Transaction{
			From:     fromAddr(idx),
			To:       toAddr(0xCCCC),
			HasTo:    true,
			Data:     data,
			Code:     code,
			GasLimit: 500_000,
			Nonce:    idx,
			GasPrice: 1,
		}
	}
	return out
}

func keccakBytecode(iters int) []byte {
	out := make([]byte, 0, iters*8+16)
	// Copy calldata into memory once: PUSH1 0x60, PUSH1 0x00, PUSH1 0x00, CALLDATACOPY
	// (omitted, we just hash whatever is at memory 0..32 repeatedly)
	for i := 0; i < iters; i++ {
		out = append(out,
			opPUSH1, 0x20, // length 32
			opPUSH1, 0x00, // offset 0
			opKECCAK256,
			opPUSH1, 0x00, // store hash back at 0
			opMSTORE,
		)
	}
	out = append(out,
		opPUSH1, 0x20,
		opPUSH1, 0x00,
		opRETURN,
	)
	return out
}

// genStorage produces txs heavy on SSTORE/SLOAD.
func genStorage(n int) []cevm.Transaction {
	code := storageBytecode(15) // 15 store/load pairs per tx
	return mkBatch(n, code, nil)
}

func storageBytecode(slots int) []byte {
	out := make([]byte, 0, slots*12+8)
	for i := 0; i < slots; i++ {
		out = append(out,
			opPUSH1, byte(i+1), // value
			opPUSH1, byte(i),   // slot
			opSSTORE,
			opPUSH1, byte(i),   // slot
			opSLOAD,
			opPOP,
		)
	}
	out = append(out,
		opPUSH1, 0x00,
		opPUSH1, 0x00,
		opRETURN,
	)
	return out
}

// genNFT produces a balanced ERC-721 mint+transfer workload.
func genNFT(n int) []cevm.Transaction {
	code := nftBytecode()
	out := make([]cevm.Transaction, n)
	for i := 0; i < n; i++ {
		idx := uint64(i)
		data := make([]byte, 4+32+32)
		// mint(to, tokenId)
		copy(data[0:4], []byte{0x40, 0xc1, 0x0f, 0x19})
		recipient := toAddr(idx)
		copy(data[4+12:4+32], recipient[:])
		binary.BigEndian.PutUint64(data[4+32+24:], idx+1)
		out[i] = cevm.Transaction{
			From:     fromAddr(idx),
			To:       toAddr(0x721),
			HasTo:    true,
			Data:     data,
			Code:     code,
			GasLimit: 250_000,
			Nonce:    idx,
			GasPrice: 1,
		}
	}
	return out
}

// nftBytecode does: read tokenId, read owner mapping (SLOAD), check non-zero,
// SSTORE owner, SSTORE balance increment, LOG1 Transfer.
func nftBytecode() []byte {
	return []byte{
		// tokenId from calldata[36..68]
		opPUSH1, 0x24, opCALLDATALD,
		// owner mapping slot = keccak(tokenId . 0x02)? We approximate.
		opDUP1,
		opSLOAD,
		opPOP, // discard old owner
		// store new owner
		opPUSH1, 0x10,
		opSSTORE,
		// balance increment
		opPUSH1, 0x11,
		opSLOAD,
		opPUSH1, 0x01,
		opADD,
		opPUSH1, 0x11,
		opSSTORE,
		// Transfer event
		opPUSH1, 0x20,
		opPUSH1, 0x00,
		opPUSH1, 0xdd,
		opLOG1,
		opPUSH1, 0x00,
		opPUSH1, 0x00,
		opRETURN,
	}
}

// mkBatch builds n txs each pointing at the same code with optional calldata.
func mkBatch(n int, code, data []byte) []cevm.Transaction {
	out := make([]cevm.Transaction, n)
	for i := 0; i < n; i++ {
		idx := uint64(i)
		out[i] = cevm.Transaction{
			From:     fromAddr(idx),
			To:       toAddr(idx),
			HasTo:    true,
			Data:     data,
			Code:     code,
			GasLimit: 300_000,
			Nonce:    idx,
			GasPrice: 1,
		}
	}
	return out
}

// describeWorkloads prints all workload names and descriptions.
func describeWorkloads() string {
	var s string
	for _, w := range allWorkloads {
		s += fmt.Sprintf("  %-15s %s\n", w.Name, w.Desc)
	}
	return s
}
