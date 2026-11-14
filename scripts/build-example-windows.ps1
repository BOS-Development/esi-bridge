# Builds location-watcher.exe, the example client, for Windows.
$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

go build -o location-watcher.exe ./examples/location-watcher

Write-Host "Built location-watcher.exe"
