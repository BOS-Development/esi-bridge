# Builds esi-proxy.exe for Windows.
#
# Requires a C toolchain (CGO) on PATH, e.g. gcc from MSYS2/MinGW or TDM-GCC:
# the app window embeds the native WebView2 webview. The WebView2 runtime
# (preinstalled on current Windows 10/11) must be present at run time.
$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

$env:CGO_ENABLED = "1"
go build -o esi-proxy.exe ./cmd/esi-proxy

Write-Host "Built esi-proxy.exe"
