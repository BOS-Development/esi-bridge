#!/usr/bin/env bash
# Builds ESI Bridge.app — required on macOS for eveauth-proxy:// login to
# work at all: Launch Services can only route a custom URL scheme to a
# proper .app bundle (CFBundleURLTypes in Info.plist), never to a bare
# binary. `go build -o esi-proxy ./cmd/esi-proxy && ./esi-proxy` still runs
# fine for everything else, but SSO login will fail until it's launched via
# this bundle at least once.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

APP_NAME="ESI Bridge"
APP_DIR="${APP_NAME}.app"
BUNDLE_ID="dev.bos-development.esi-proxy"

rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

# App icon, from the same image the tray and UI use. sips scales it to each
# size an .icns needs and iconutil packs the set.
ICONSET="$(mktemp -d)/AppIcon.iconset"
mkdir -p "$ICONSET"
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" internal/assets/bridge.png --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z "$double" "$double" internal/assets/bridge.png --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP_DIR/Contents/Resources/AppIcon.icns"
rm -rf "$(dirname "$ICONSET")"

# Both this binary's internal/urlscheme package and fyne.io/systray compile
# Objective-C and link Cocoa, so Go's linker appends -lobjc once per package.
# The newer Apple linker (Xcode 15+) warns "ignoring duplicate libraries:
# '-lobjc'" about the resulting dup — harmless, but noisy. Silence it with the
# new linker's own flag, but only when the linker actually supports it (older
# linkers both lack the flag and never emit the warning). Go's cgo flag
# allowlist rejects this flag in a #cgo directive, so it's passed here via
# -extldflags instead. Empty string = flag unsupported; safe under `set -u`.
LDFLAGS=""
if ! ld -no_warn_duplicate_libraries 2>&1 | grep -q "unknown option"; then
	LDFLAGS="-extldflags=-Wl,-no_warn_duplicate_libraries"
fi

# The bundled EVE SSO Client ID is read from a .clientid file at the repo
# root (gitignored). Without it the build has no bundled Client ID and users
# must register their own application.
if [[ -f .clientid ]]; then
	CLIENT_ID="$(tr -d '[:space:]' < .clientid)"
	if [[ ! "$CLIENT_ID" =~ ^[A-Za-z0-9]+$ ]]; then
		echo "error: .clientid must contain only the EVE SSO Client ID (letters and digits)" >&2
		exit 1
	fi
	LDFLAGS="${LDFLAGS} -X github.com/BOS-Development/esi-proxy/internal/buildinfo.BundledClientID=${CLIENT_ID}"
else
	echo "warning: no .clientid file; building without a bundled Client ID (users must set a custom one)" >&2
fi

# Builds a development build by default, which adds developer-only options to
# the Settings tab. RELEASE=1 makes a production release (-tags release).
TAGS_ARG=""
if [[ "${RELEASE:-}" == "1" ]]; then
	TAGS_ARG="-tags=release"
fi

# VERSION sets the bundle version shown in Finder (default 1.0). When it's
# given, it's also stamped into the binary (buildinfo.Version), which the
# auto-updater compares against GitHub releases; without it the build is
# unversioned and never checks for updates.
if [[ -n "${VERSION:-}" ]]; then
	if [[ ! "$VERSION" =~ ^[0-9]+(\.[0-9]+){0,2}$ ]]; then
		echo "error: VERSION must be numeric, e.g. 1.0.42 (got '$VERSION')" >&2
		exit 1
	fi
	LDFLAGS="${LDFLAGS} -X github.com/BOS-Development/esi-proxy/internal/buildinfo.Version=${VERSION}"
fi
VERSION="${VERSION:-1.0}"

# Oldest macOS the binary supports; keeps Intel and Apple Silicon builds in step.
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"

BIN="$APP_DIR/Contents/MacOS/esi-bridge"
if [[ "${UNIVERSAL:-}" == "1" ]]; then
	# UNIVERSAL=1 builds a fat binary that runs natively on both Apple Silicon
	# and Intel Macs. cgo is off by default when cross-compiling, so it's
	# forced on; Apple's clang targets either arch via the -arch flag Go passes.
	for arch in arm64 amd64; do
		CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" \
			go build ${TAGS_ARG} -ldflags "${LDFLAGS}" -o "$BIN-$arch" ./cmd/esi-proxy
	done
	lipo -create -output "$BIN" "$BIN-arm64" "$BIN-amd64"
	rm -f "$BIN-arm64" "$BIN-amd64"
else
	go build ${TAGS_ARG} -ldflags "${LDFLAGS}" -o "$BIN" ./cmd/esi-proxy
fi

cat > "$APP_DIR/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>${APP_NAME}</string>
	<key>CFBundleIdentifier</key>
	<string>${BUNDLE_ID}</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundleExecutable</key>
	<string>esi-bridge</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${VERSION}</string>
	<key>CFBundleVersion</key>
	<string>${VERSION}</string>
	<key>LSMinimumSystemVersion</key>
	<string>${MACOSX_DEPLOYMENT_TARGET}</string>
	<key>LSUIElement</key>
	<true/>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleURLName</key>
			<string>${BUNDLE_ID}.auth-callback</string>
			<key>CFBundleURLSchemes</key>
			<array>
				<string>eveauth-proxy</string>
			</array>
		</dict>
	</array>
</dict>
</plist>
PLIST

# Ad-hoc sign the whole bundle so its signature covers Info.plist (Apple
# Silicon refuses to run unsigned code, and a bundle whose binary was signed
# before Info.plist existed reads as damaged). This is not a Developer ID
# signature: downloaded copies still need Gatekeeper's "Open Anyway" once.
codesign --force --deep --sign - "$APP_DIR"

LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"
"$LSREGISTER" -f "$PWD/$APP_DIR"

echo "Built $APP_DIR and registered it for eveauth-proxy:// with Launch Services."
echo "Run it with: open \"$APP_DIR\""
