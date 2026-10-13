# Builds esi-proxy.exe for Windows.
$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

go build -o esi-proxy.exe ./cmd/esi-proxy

Write-Host "Built esi-proxy.exe"
