#!/bin/bash
set -e

CHAIN=${1:-lux}
NODES=${2:-5}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$SCRIPT_DIR/../docker"

echo "Starting $NODES-node $CHAIN network..."

case $CHAIN in
  lux)
    docker compose -f "$DOCKER_DIR/lux/compose.yml" up -d
    echo "Waiting for Lux nodes to sync..."
    sleep 10
    echo "Lux network ready at http://localhost:9650"
    ;;
  avalanche)
    docker compose -f "$DOCKER_DIR/avalanche/compose.yml" up -d
    echo "Waiting for Avalanche nodes to sync..."
    sleep 10
    echo "Avalanche network ready at http://localhost:9660"
    ;;
  geth)
    docker compose -f "$DOCKER_DIR/geth/compose.yml" up -d
    echo "Waiting for Geth nodes to sync..."
    sleep 5
    echo "Geth network ready at http://localhost:8545"
    ;;
  op-stack)
    docker compose -f "$DOCKER_DIR/op-stack/compose.yml" up -d
    echo "Waiting for OP Stack nodes to sync..."
    sleep 15
    echo "OP Stack network ready at http://localhost:8546"
    ;;
  solana)
    docker compose -f "$DOCKER_DIR/solana/compose.yml" up -d
    echo "Waiting for Solana validators..."
    sleep 20
    echo "Solana network ready at http://localhost:8899"
    ;;
  all)
    for c in lux avalanche geth op-stack solana; do
      $0 $c $NODES
    done
    ;;
  *)
    echo "Unknown chain: $CHAIN"
    echo "Usage: $0 [lux|avalanche|geth|op-stack|solana|all] [nodes]"
    exit 1
    ;;
esac
