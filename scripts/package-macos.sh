#!/usr/bin/env bash
# Builds the macOS installer: dist/esi-bridge-<version>-macos-universal.dmg
#
# A drag-to-install disk image holding a release, universal (Apple Silicon +
# Intel) "ESI Bridge.app" next to an Applications shortcut.
#
#   ./scripts/package-macos.sh 1.0.42
#
# Put the EVE SSO Client ID in .clientid at the repo root first, or the app
# ships without a bundled Client ID.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${1:-}"
if [[ ! "$VERSION" =~ ^[0-9]+(\.[0-9]+){0,2}$ ]]; then
	echo "usage: $0 <version>   (numeric, e.g. 1.0.42)" >&2
	exit 1
fi

APP_DIR="ESI Bridge.app"
DMG="dist/esi-bridge-${VERSION}-macos-universal.dmg"
STAGE="dist/macos-dmg"

RELEASE=1 UNIVERSAL=1 VERSION="$VERSION" ./scripts/build-macos-app.sh

rm -rf "$STAGE" "$DMG"
mkdir -p "$STAGE"
cp -R "$APP_DIR" "$STAGE/"
ln -s /Applications "$STAGE/Applications"

# hdiutil occasionally fails with "Resource busy" on CI runners; retry.
for attempt in 1 2 3 4 5; do
	if hdiutil create -volname "ESI Bridge" -srcfolder "$STAGE" -ov -format UDZO "$DMG"; then
		break
	fi
	if [[ $attempt == 5 ]]; then
		echo "error: hdiutil create failed" >&2
		exit 1
	fi
	sleep $((attempt * 5))
done

rm -rf "$STAGE"
echo "Built $DMG"
