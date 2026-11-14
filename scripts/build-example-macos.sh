#!/usr/bin/env bash
# Builds the location-watcher example client for macOS. Unlike the proxy, this
# example registers no eveauth-proxy:// URL scheme, so it needs no .app bundle
# — a bare binary runs fine.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

go build -o location-watcher ./examples/location-watcher

echo "Built location-watcher"
