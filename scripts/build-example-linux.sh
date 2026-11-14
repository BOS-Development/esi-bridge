#!/usr/bin/env bash
# Builds the location-watcher example client for Linux. It's a plain CLI with
# no custom URL scheme, so — unlike the proxy on macOS — no .app bundle is
# needed on any platform; a bare `go build` is enough everywhere.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

go build -o location-watcher ./examples/location-watcher

echo "Built location-watcher"
