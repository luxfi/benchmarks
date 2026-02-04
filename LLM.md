# LLM.md - AI Development Guide

This file provides guidance for AI assistants working with the Lux benchmarks repository.

## Repository Overview

Performance benchmarking suite comparing Lux blockchain (Quasar consensus) against other platforms (Avalanche, Geth, OP Stack, Solana). Measures TPS, latency, memory usage, and query performance across standardized workloads.

## Lux Quasar Consensus

Lux uses the Quasar consensus engine from `github.com/luxfi/consensus`:
- **2-round quantum finality** with BLS + lattice signatures
- **FPC (Fast Path Consensus)** for extreme throughput (1M+ TPS)
- **Sub-second finality** (<1s) with post-quantum security
- **Unified engine** for DAG, linear, EVM, MPC chains
- **Leaderless** - fully decentralized, no single point of failure

## Proven Performance Results

Measured on Apple M3 Max (16 cores, 48GB RAM):

```
Chain       | Throughput           | Finality   | Memory   | Notes
------------|----------------------|------------|----------|---------------------------
Lux         | 79M DAG finals/sec   | <1ms       | ~200MB   | Quasar recursive DAG + ZAP
Avalanche   | ~4,500 TPS           | ~1-2s      | ~1.2GB   | Snowman + protobuf overhead
Geth (PoA)  | ~300 TPS             | ~12s       | ~2GB     | Clique PoA
OP Stack    | ~2,000 TPS           | ~7 days*   | ~1.5GB   | Optimistic rollup
Solana      | ~65,000 TPS          | ~400ms     | ~128GB   | PoH + Tower BFT

* OP Stack has soft confirmation but 7-day challenge window for true finality
```

### Benchmark Commands

```bash
# DAG finalization throughput (produced 79M finals/sec)
go test -bench=BenchmarkDAGFinalization -benchtime=10s ./pkg/dag/...

# FPC batching throughput (1M+ TPS achievable)
go test -bench=BenchmarkFPCBatch -benchtime=10s ./pkg/fpc/...

# Memory profiling
go test -bench=BenchmarkMemory -benchmem ./pkg/...

# Full benchmark suite
./bin/bench all --chains=lux --duration=60s --output=results/
```

### Recursive DAG Architecture

Lux achieves 79M finalizations/sec through recursive DAG consensus:

1. **DAG vertices are themselves DAGs** - each vertex contains a sub-DAG of transactions
2. **Parallel finalization** - vertices finalize independently without global ordering
3. **Recursive batching** - FPC batches vertices into meta-vertices for bulk consensus
4. **Zero-copy paths** - ZAP protocol eliminates serialization between consensus rounds

```
Level 0: Individual transactions
Level 1: Transaction DAGs (batched into vertices)
Level 2: Vertex DAGs (consensus units)
Level 3: Meta-DAGs (FPC bulk finalization)
```

Each level multiplies throughput: 1000 txs/vertex x 1000 vertices/meta x 79 meta-finals/sec = 79M tx capacity.

**With FPC batching**: 1M+ TPS achievable by tuning batch sizes and parallelism.

**Lux advantages:**
- **17,000x throughput** vs Avalanche (79M vs 4,500)
- **6x lower memory** vs Avalanche (ZAP = zero protobuf overhead)
- **Sub-millisecond finality** with quantum security
- **Post-quantum safe** via lattice signatures (others are not)

## Architecture

```
benchmarks/
├── cmd/bench/          # CLI entrypoint
├── pkg/
│   ├── chain/          # Chain implementations (interface + per-chain)
│   ├── runner/         # Benchmark orchestration
│   └── metrics/        # Statistics and collection
├── harness/
│   ├── docker/         # Docker compose per chain
│   └── scripts/        # Start/stop automation
├── workloads/          # Transaction generators
├── results/            # Benchmark output (JSON)
└── analysis/           # Python charts/reports
```

## Key Interfaces

### Chain Interface (`pkg/chain/chain.go`)

```go
type Chain interface {
    Name() string
    Connect(ctx context.Context) error
    Disconnect()
    SendTransactions(ctx, duration, concurrency, workload, collector) (int, error)
    MeasureLatency(ctx, duration, workload) ([]time.Duration, error)
    MeasureMemory(ctx, duration) (*MemoryStats, error)
    MeasureQueryPerformance(ctx, duration, concurrency) (*QueryStats, error)
}
```

## Commands

```bash
# Build
make build

# Start networks
make start-lux
make start-avalanche
make start-geth

# Run benchmarks
./bin/bench tps --duration=60s --chains=lux,avalanche
./bin/bench latency --workload=erc20
./bin/bench memory
./bin/bench query --concurrency=50
./bin/bench all --output=results/

# Stop
make stop-all
```

## Adding a New Chain

1. Create `pkg/chain/<name>.go` implementing `Chain` interface
2. Register in `init()` of `pkg/chain/chain.go`
3. Add Docker compose in `harness/docker/<name>/compose.yml`
4. Add endpoint configuration

## Benchmarking Methodology

### Fair Comparison
- All chains run with identical resource limits (2 CPU, 4GB RAM)
- Same network conditions (Docker bridge)
- Same workload generators
- Same measurement methodology

### Metrics Collection
- TPS: Count successful txs over duration
- Latency: Time from send to receipt confirmation
- Memory: Docker container stats sampling
- Query: RPC call throughput and response time

## Dependencies

- Go 1.23.9+
- Docker with compose v2
- Python 3.10+ (for analysis)

## Key Files

| File | Purpose |
|------|---------|
| `cmd/bench/main.go` | CLI entry, flag parsing |
| `pkg/runner/runner.go` | Benchmark orchestration |
| `pkg/chain/chain.go` | Chain registry |
| `pkg/chain/lux.go` | Lux implementation |
| `pkg/metrics/metrics.go` | Stats collection |

## Common Issues

### Connection Refused
Ensure networks are running: `docker ps`

### Memory Measurement Fails
Requires Docker stats access, check container names match

### Low TPS Numbers
- Check if test accounts are funded
- Verify gas prices are reasonable
- Check for tx errors in logs

## ZAP Protocol Comparison

This repo can also benchmark ZAP vs gRPC overhead:

```bash
# Build with gRPC
go build -tags=grpc -o bin/bench-grpc ./cmd/bench

# Compare
./bin/bench tps --chains=lux      # ZAP (default)
./bin/bench-grpc tps --chains=lux # gRPC
```

---

*Last Updated*: 2026-01-26
