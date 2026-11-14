#!/usr/bin/env bash
# Builds the esi-proxy binary for Linux.
#
# Requires a C toolchain (CGO): the app window embeds the native WebKitGTK
# webview and the menu-bar/tray presence is native too. Install the dev
# packages first, e.g. on Debian/Ubuntu:
#   sudo apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

CGO_ENABLED=1 go build -o esi-proxy ./cmd/esi-proxy

echo "Built esi-proxy"
