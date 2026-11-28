#!/usr/bin/env bash
# Regenerates cmd/esi-bridge/rsrc_windows_amd64.syso from packaging/windows/app.rc
# (the app icon in internal/assets/bridge.ico). go build links any .syso in the
# package automatically, so the generated file is committed and normal builds
# need no extra tools. Requires windres from MinGW binutils (e.g. MSYS2).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../packaging/windows"
windres --target=pe-x86-64 -i app.rc -O coff -o ../../cmd/esi-bridge/rsrc_windows_amd64.syso
echo "Wrote cmd/esi-bridge/rsrc_windows_amd64.syso"
