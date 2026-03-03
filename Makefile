.PHONY: all build test bench-all bench-tps bench-latency bench-memory bench-query
.PHONY: start-lux start-avalanche start-geth start-op start-solana stop-all clean

# Configuration
NODES ?= 5
DURATION ?= 60s
WORKLOAD ?= raw

# Build
all: build

build:
	go build -o bin/bench ./cmd/bench

test:
	go test -v ./...

# Start networks
start-lux:
	./harness/scripts/start.sh lux $(NODES)

start-avalanche:
	./harness/scripts/start.sh avalanche $(NODES)

start-geth:
	./harness/scripts/start.sh geth $(NODES)

start-op:
	./harness/scripts/start.sh op-stack $(NODES)

start-solana:
	./harness/scripts/start.sh solana $(NODES)

stop-all:
	./harness/scripts/stop.sh

# Individual benchmarks
bench-tps: build
	./bin/bench tps --duration=$(DURATION) --workload=$(WORKLOAD)

bench-latency: build
	./bin/bench latency --duration=$(DURATION) --workload=$(WORKLOAD)

bench-memory: build
	./bin/bench memory --duration=$(DURATION)

bench-query: build
	./bin/bench query --duration=$(DURATION)

bench-dag: build
	./bin/bench dag --txs=1000 --proofs=100 --runs=3 --seed=42

# Full benchmark suite
bench-all: build
	@echo "=== Lux Blockchain Benchmarks ==="
	@echo "Running full benchmark suite against all chains..."
	@mkdir -p results/$$(date +%Y-%m-%d)
	./bin/bench all --duration=$(DURATION) --output=results/$$(date +%Y-%m-%d)/

# Analysis
charts:
	cd analysis && python charts.py

report:
	cd analysis && python report.py

# Cleanup
clean:
	rm -rf bin/
	docker compose -f harness/docker/lux/compose.yml down -v 2>/dev/null || true
	docker compose -f harness/docker/avalanche/compose.yml down -v 2>/dev/null || true
	docker compose -f harness/docker/geth/compose.yml down -v 2>/dev/null || true
	docker compose -f harness/docker/op-stack/compose.yml down -v 2>/dev/null || true
	docker compose -f harness/docker/solana/compose.yml down -v 2>/dev/null || true
