#!/usr/bin/env bash
# Builds the ESI Bridge binary (esi-proxy) for Linux.
#
# Requires a C toolchain (CGO): the app window embeds the native WebKitGTK
# webview and the menu-bar/tray presence is native too. Install the dev
# packages first, e.g. on Debian/Ubuntu:
#   sudo apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev
#
# Builds a development build by default, which adds developer-only options to
# the Settings tab. Set RELEASE=1 for a production release (-tags release).
#
# The bundled EVE SSO Client ID is read from a .clientid file at the repo
# root (gitignored). Without it the build has no bundled Client ID and users
# must register their own application.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

TAGS=()
if [[ "${RELEASE:-}" == "1" ]]; then
	TAGS=(-tags release)
fi

LDFLAGS=""
if [[ -f .clientid ]]; then
	CLIENT_ID="$(tr -d '[:space:]' < .clientid)"
	if [[ ! "$CLIENT_ID" =~ ^[A-Za-z0-9]+$ ]]; then
		echo "error: .clientid must contain only the EVE SSO Client ID (letters and digits)" >&2
		exit 1
	fi
	LDFLAGS="-X github.com/BOS-Development/esi-proxy/internal/buildinfo.BundledClientID=${CLIENT_ID}"
else
	echo "warning: no .clientid file; building without a bundled Client ID (users must set a custom one)" >&2
fi

CGO_ENABLED=1 go build "${TAGS[@]}" -ldflags "$LDFLAGS" -o esi-proxy ./cmd/esi-proxy

if [[ "${RELEASE:-}" == "1" ]]; then
	echo "Built esi-proxy (release build)"
else
	echo "Built esi-proxy (development build)"
fi
