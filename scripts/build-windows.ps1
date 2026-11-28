# Builds esi-bridge.exe for Windows.
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
# -Output sets the output path (default: esi-bridge.exe at the repo root).
#
# -Version stamps the release version into the binary (buildinfo.Version),
# which the auto-updater compares against GitHub releases. Without it the
# build is unversioned and never checks for updates.
#
# The bundled EVE SSO Client ID is read from a .clientid file at the repo
# root (gitignored). Without it the build has no bundled Client ID and users
# must register their own application.
param(
	[switch]$Release,
	[switch]$Gui,
	[string]$Output = "esi-bridge.exe",
	[string]$Version = ""
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

if ($Version) {
	if ($Version -notmatch '^\d+(\.\d+){0,3}$') {
		throw "-Version must be numeric, e.g. 1.0.42 (got '$Version')"
	}
	$ldflags += "-X github.com/BOS-Development/esi-bridge/internal/buildinfo.Version=$Version"
}

if (Test-Path ".clientid") {
	$clientID = (Get-Content -Raw ".clientid") -replace '\s', ''
	if ($clientID -notmatch '^[A-Za-z0-9]+$') {
		throw ".clientid must contain only the EVE SSO Client ID (letters and digits)"
	}
	$ldflags += "-X github.com/BOS-Development/esi-bridge/internal/buildinfo.BundledClientID=$clientID"
} else {
	Write-Warning "no .clientid file; building without a bundled Client ID (users must set a custom one)"
}

if ($ldflags.Count -gt 0) {
	$buildArgs += @("-ldflags", ($ldflags -join " "))
}

$buildArgs += @("-o", $Output, "./cmd/esi-bridge")

$env:CGO_ENABLED = "1"
& go @buildArgs
if ($LASTEXITCODE -ne 0) {
	throw "go build failed with exit code $LASTEXITCODE"
}

$kind = if ($Release) { "release build" } else { "development build" }
if ($Gui) { $kind += ", GUI subsystem" }
if ($Version) { $kind += ", version $Version" }
Write-Host "Built $Output ($kind)"
