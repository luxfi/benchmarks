# Lux Blockchain Benchmarks

Comprehensive performance benchmarking suite for comparing Lux against other blockchain platforms.

## Supported Chains

| Chain | Type | Consensus |
|-------|------|-----------|
| **Lux** | Multi-chain | Quasar (2-round quantum finality + FPC) |
| **Avalanche** | Multi-chain | Snowman |
| **Geth** | L1 | PoA (Clique) |
| **OP Stack** | L2 | Optimistic Rollup |
| **Solana** | L1 | PoH + Tower BFT |

## Benchmarks

| Metric | Description |
|--------|-------------|
| **TPS** | Sustained transactions per second under load |
| **Latency** | P50/P95/P99 transaction confirmation time |
| **Memory** | Node memory usage (avg/peak) under load |
| **Query** | RPC queries per second, response latency |
| **Finality** | Time to irreversible confirmation |

## Quick Start

```bash
# Build benchmark tool
make build

# Start a 5-node Lux network
make start-lux

# Run TPS benchmark
make bench-tps DURATION=60s

# Run all benchmarks
make bench-all

# Stop all networks
make stop-all
```

## Running Individual Chains

```bash
# Start specific chain
./harness/scripts/start.sh lux 5
./harness/scripts/start.sh avalanche 5
./harness/scripts/start.sh geth 5

# Run benchmarks
./bin/bench tps --chains=lux,avalanche --duration=120s
./bin/bench latency --chains=lux --workload=erc20
./bin/bench memory --chains=all
./bin/bench query --chains=lux,geth --concurrency=100
```

## Workloads

| Workload | Description |
|----------|-------------|
| `raw` | Simple ETH/native token transfers |
| `erc20` | ERC20 token transfers |
| `uniswap` | DEX swap transactions |
| `nft` | ERC721 mint operations |

## Results

Results are saved to `results/<date>/` in JSON format:

```json
{
  "chain": "lux",
  "benchmark": "tps",
  "timestamp": "2026-01-26T12:00:00Z",
  "duration": "60s",
  "metrics": {
    "tps": 4500.5,
    "total_txs": 270030,
    "failed_txs": 15
  }
}
```

## Generating Charts

```bash
# Generate comparison charts
make charts

# Generate markdown report
make report
```

## Hardware Requirements

For fair comparison, all chains run with identical resource limits:
- **CPU**: 2 cores per node
- **Memory**: 4GB per node
- **Network**: Docker bridge (same latency)

## CI/CD

Benchmarks run nightly via GitHub Actions. Results are posted to the [benchmark dashboard](https://benchmarks.lux.network).

## Adding New Chains

1. Create chain implementation in `pkg/chain/<name>.go`
2. Implement the `Chain` interface
3. Add Docker compose in `harness/docker/<name>/`
4. Register in `pkg/chain/chain.go`

## License

Copyright (C) 2019-2025, Lux Industries Inc. All rights reserved.
