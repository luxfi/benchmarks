# EVM Benchmark Suite
# Compares: lux/evm (Go) vs lux/evmgpu (Go parallel) vs hanzo/evm (Rust reth)
#
# Prerequisites: `cd ~/work/lux/universe && make up` (starts luxd with all VMs)

SHELL := /bin/bash

LUX_RPC  := http://127.0.0.1:9660/ext/bc/C/rpc
GPU_RPC  := http://127.0.0.1:9670/ext/bc/C/rpc
RETH_RPC := http://127.0.0.1:9680/ext/bc/C/rpc
TXS      := 100
CHAIN_ID := 31337

.PHONY: build
build: ## Build benchmark binary
	@GOPRIVATE="*" GONOSUMDB="*" GONOSUMCHECK="*" go build -o evm-bench .

.PHONY: bench-lux
bench-lux: build ## Benchmark lux/evm (Go, sequential)
	./evm-bench --lux-rpc=$(LUX_RPC) --txs=$(TXS) --chain-id=$(CHAIN_ID)

.PHONY: bench-gpu
bench-gpu: build ## Benchmark lux/evmgpu (Go, parallel Block-STM)
	./evm-bench --lux-rpc=$(GPU_RPC) --txs=$(TXS) --chain-id=$(CHAIN_ID)

.PHONY: bench-reth
bench-reth: build ## Benchmark hanzo/evm (Rust, reth/revm)
	./evm-bench --reth-rpc=$(RETH_RPC) --txs=$(TXS) --chain-id=$(CHAIN_ID)

.PHONY: bench-all
bench-all: build ## Benchmark all EVMs (requires all 3 running)
	./evm-bench --lux-rpc=$(LUX_RPC) --gpu-rpc=$(GPU_RPC) --reth-rpc=$(RETH_RPC) --txs=$(TXS) --chain-id=$(CHAIN_ID)

.PHONY: bench-compare
bench-compare: build ## Compare lux vs gpu (most common)
	./evm-bench --lux-rpc=$(LUX_RPC) --gpu-rpc=$(GPU_RPC) --txs=$(TXS) --chain-id=$(CHAIN_ID)

.PHONY: quick
quick: build ## Quick 10-tx test against lux EVM
	./evm-bench --lux-rpc=$(LUX_RPC) --txs=10 --chain-id=$(CHAIN_ID) --workload=transfer

DIRECT_TXS := 1000
DIRECT_RUNS := 3

.PHONY: build-direct
build-direct: ## Build direct EVM benchmark binary
	@GOPRIVATE="*" GONOSUMDB="*" GONOSUMCHECK="*" go build -o direct-bench ./cmd/direct-bench/

.PHONY: bench-direct
bench-direct: build-direct ## Run direct EVM benchmark (ETH transfers)
	./direct-bench --workload=transfer --txs=$(DIRECT_TXS) --runs=$(DIRECT_RUNS)

.PHONY: bench-direct-all
bench-direct-all: build-direct ## Run all direct EVM workloads
	./direct-bench --workload=all --txs=$(DIRECT_TXS) --runs=$(DIRECT_RUNS)

.PHONY: bench-parallel
bench-parallel: build-direct ## Run parallel sig recovery comparison
	./direct-bench --workload=all --txs=$(DIRECT_TXS) --runs=$(DIRECT_RUNS) --parallel

DAG_TXS     := 10000
DAG_SEED    := 42
DAG_RUNS    := 3
DAG_WORK    := all

.PHONY: bench-dag
bench-dag: ## Run DAG EVM benchmark matrix (erc20, amm, arb, random)
	@GOPRIVATE="*" GONOSUMDB="*" GONOSUMCHECK="*" go test -v -run TestRunDAGBenchMatrix ./bench/ -count=1 -timeout=600s

.PHONY: test
test: ## Run all tests
	@GOPRIVATE="*" GONOSUMDB="*" GONOSUMCHECK="*" go test -v ./bench/ -count=1

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
