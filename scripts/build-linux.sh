#!/usr/bin/env bash
# Builds the esi-proxy binary for Linux.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

go build -o esi-proxy ./cmd/esi-proxy

echo "Built esi-proxy"
