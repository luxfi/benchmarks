#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$SCRIPT_DIR/../docker"

echo "Stopping all benchmark networks..."

for chain in lux avalanche geth op-stack solana; do
  if [ -f "$DOCKER_DIR/$chain/compose.yml" ]; then
    docker compose -f "$DOCKER_DIR/$chain/compose.yml" down -v 2>/dev/null || true
    echo "Stopped $chain"
  fi
done

echo "All networks stopped."
