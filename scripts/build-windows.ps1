# Builds esi-proxy.exe for Windows.
#
# Requires a C toolchain (CGO) on PATH, e.g. gcc from MSYS2/MinGW or TDM-GCC:
# the app window embeds the native WebView2 webview. The WebView2 runtime
# (preinstalled on current Windows 10/11) must be present at run time.
#
# Builds a development build by default, which adds developer-only options to
# the Settings tab. Pass -Release for a production release (-tags release).
#
# Pass -Gui to link for the Windows GUI subsystem (-H=windowsgui), so launching
# it from Explorer or a shortcut doesn't open a console window. The CLI
# subcommands (export/import/mdns) need a console, so they won't work from a
# -Gui build; scripts/package-windows.ps1 ships both variants.
#
# -Output sets the output path (default: esi-proxy.exe at the repo root).
#
# The bundled EVE SSO Client ID is read from a .clientid file at the repo
# root (gitignored). Without it the build has no bundled Client ID and users
# must register their own application.
param(
	[switch]$Release,
	[switch]$Gui,
	[string]$Output = "esi-proxy.exe"
)

$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

$buildArgs = @("build")
if ($Release) {
	$buildArgs += @("-tags", "release")
}

$ldflags = @()
if ($Gui) {
	$ldflags += "-H=windowsgui"
}

if (Test-Path ".clientid") {
	$clientID = (Get-Content -Raw ".clientid") -replace '\s', ''
	if ($clientID -notmatch '^[A-Za-z0-9]+$') {
		throw ".clientid must contain only the EVE SSO Client ID (letters and digits)"
	}
	$ldflags += "-X github.com/BOS-Development/esi-proxy/internal/buildinfo.BundledClientID=$clientID"
} else {
	Write-Warning "no .clientid file; building without a bundled Client ID (users must set a custom one)"
}

if ($ldflags.Count -gt 0) {
	$buildArgs += @("-ldflags", ($ldflags -join " "))
}

$buildArgs += @("-o", $Output, "./cmd/esi-proxy")

$env:CGO_ENABLED = "1"
& go @buildArgs
if ($LASTEXITCODE -ne 0) {
	throw "go build failed with exit code $LASTEXITCODE"
}

$kind = if ($Release) { "release build" } else { "development build" }
if ($Gui) { $kind += ", GUI subsystem" }
Write-Host "Built $Output ($kind)"
