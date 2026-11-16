# Builds esi-proxy.exe for Windows.
#
# Requires a C toolchain (CGO) on PATH, e.g. gcc from MSYS2/MinGW or TDM-GCC:
# the app window embeds the native WebView2 webview. The WebView2 runtime
# (preinstalled on current Windows 10/11) must be present at run time.
#
# Builds a development build by default, which adds developer-only options to
# the Settings tab. Pass -Release for a production release (-tags release).
#
# The bundled EVE SSO Client ID is read from a .clientid file at the repo
# root (gitignored). Without it the build has no bundled Client ID and users
# must register their own application.
param([switch]$Release)

$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

$buildArgs = @("build")
if ($Release) {
	$buildArgs += @("-tags", "release")
}

if (Test-Path ".clientid") {
	$clientID = (Get-Content -Raw ".clientid") -replace '\s', ''
	if ($clientID -notmatch '^[A-Za-z0-9]+$') {
		throw ".clientid must contain only the EVE SSO Client ID (letters and digits)"
	}
	$buildArgs += @("-ldflags", "-X github.com/BOS-Development/esi-proxy/internal/buildinfo.BundledClientID=$clientID")
} else {
	Write-Warning "no .clientid file; building without a bundled Client ID (users must set a custom one)"
}

$buildArgs += @("-o", "esi-proxy.exe", "./cmd/esi-proxy")

$env:CGO_ENABLED = "1"
& go @buildArgs
if ($LASTEXITCODE -ne 0) {
	throw "go build failed with exit code $LASTEXITCODE"
}

if ($Release) {
	Write-Host "Built esi-proxy.exe (release build)"
} else {
	Write-Host "Built esi-proxy.exe (development build)"
}
