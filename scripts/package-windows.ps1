# Builds the Windows installer: dist\esi-proxy-<version>-windows-x64-setup.exe
#
# Compiles two release binaries and wraps them with Inno Setup
# (packaging\windows\esi-proxy.iss):
#   esi-proxy.exe      GUI subsystem: what the shortcuts and the
#                      eveauth-proxy:// handler launch (no console window)
#   esi-proxy-cli.exe  console subsystem: for `export` / `import` / `mdns`
#
# Requires a C toolchain (see scripts/build-windows.ps1) and Inno Setup 6
# (https://jrsoftware.org/isinfo.php, or `choco install innosetup`).
#
# Put the EVE SSO Client ID in .clientid at the repo root first, or the
# installer ships without a bundled Client ID.
param(
	[Parameter(Mandatory = $true)][string]$Version
)

$ErrorActionPreference = "Stop"

if ($Version -notmatch '^\d+(\.\d+){0,3}$') {
	throw "-Version must be numeric, e.g. 1.0.42 (got '$Version')"
}

$root = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $root

$buildDir = Join-Path $root "dist\windows"
New-Item -ItemType Directory -Force $buildDir | Out-Null

& (Join-Path $PSScriptRoot "build-windows.ps1") -Release -Gui -Version $Version -Output (Join-Path $buildDir "esi-proxy.exe")
& (Join-Path $PSScriptRoot "build-windows.ps1") -Release -Version $Version -Output (Join-Path $buildDir "esi-proxy-cli.exe")

$iscc = (Get-Command "ISCC.exe" -ErrorAction SilentlyContinue).Source
if (-not $iscc) {
	foreach ($dir in @(${env:ProgramFiles(x86)}, $env:ProgramFiles, (Join-Path $env:LOCALAPPDATA "Programs"))) {
		if (-not $dir) { continue }
		$candidate = Join-Path $dir "Inno Setup 6\ISCC.exe"
		if (Test-Path $candidate) { $iscc = $candidate; break }
	}
}
if (-not $iscc) {
	throw "ISCC.exe (Inno Setup 6) not found; install it from https://jrsoftware.org/isinfo.php or with 'choco install innosetup'"
}

$outDir = Join-Path $root "dist"
& $iscc "/DAppVersion=$Version" "/DBuildDir=$buildDir" "/DOutputDir=$outDir" (Join-Path $root "packaging\windows\esi-proxy.iss")
if ($LASTEXITCODE -ne 0) {
	throw "ISCC failed with exit code $LASTEXITCODE"
}

Write-Host "Built dist\esi-proxy-$Version-windows-x64-setup.exe"
