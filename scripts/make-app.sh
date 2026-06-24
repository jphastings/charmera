#!/usr/bin/env bash
# Build Charmera.app: a menu-bar app with the charmera CLI/daemon bundled inside,
# as a universal (Intel + Apple Silicon) binary, then sign, (optionally) notarize
# and zip it.
#
# Signing degrades the same way scripts/macos-sign.sh does, so a build never
# breaks before signing is configured:
#   - no Developer ID credentials -> ad-hoc sign only (runs locally; not notarized)
#   - credentials present          -> Developer ID sign + notarize + staple
#
# Credentials (GitHub Actions secrets), reusing the same ones quill uses:
#   QUILL_SIGN_P12       base64 of the Developer ID Application .p12
#   QUILL_SIGN_PASSWORD  password for that .p12
#   QUILL_NOTARY_KEY     base64 of the App Store Connect API .p8
#   QUILL_NOTARY_KEY_ID  the API key's Key ID
#   QUILL_NOTARY_ISSUER  the API key's Issuer ID
#
# Usage: VERSION=1.2.3 scripts/make-app.sh
set -euo pipefail

VERSION="${VERSION:-dev}"
SHORT_VERSION="${VERSION#v}"
DIST="${DIST:-dist}"
APP="$DIST/Charmera.app"
BUNDLE_ID="com.charmera.app"
ZIP="$DIST/Charmera_${VERSION}_darwin_universal.zip"

CLI_LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X main.date=$(git log -1 --format=%cI 2>/dev/null || echo unknown)"

# build_universal <main-pkg> <output> [ldflags]
build_universal() {
	local main="$1" out="$2" ldflags="${3:-"-s -w"}"
	echo "building universal $out"
	# GOARCH and the clang -arch must agree, or cgo passes conflicting -arch flags.
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" \
		go build -trimpath -ldflags "$ldflags" -o "$DIST/.amd64.tmp" "$main"
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="clang -arch arm64" \
		go build -trimpath -ldflags "$ldflags" -o "$DIST/.arm64.tmp" "$main"
	lipo -create -output "$out" "$DIST/.amd64.tmp" "$DIST/.arm64.tmp"
	rm -f "$DIST/.amd64.tmp" "$DIST/.arm64.tmp"
}

echo "assembling $APP (version $SHORT_VERSION)"
rm -rf "$APP" "$ZIP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# The CLI lives in Resources, not MacOS: the bundle executable is "Charmera" and
# the filesystem is case-insensitive, so "charmera" in the same dir would collide.
build_universal . "$APP/Contents/Resources/charmera" "$CLI_LDFLAGS"
build_universal ./cmd/charmera-menu "$APP/Contents/MacOS/Charmera"

cat >"$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Charmera</string>
	<key>CFBundleDisplayName</key>
	<string>Charmera</string>
	<key>CFBundleIdentifier</key>
	<string>${BUNDLE_ID}</string>
	<key>CFBundleExecutable</key>
	<string>Charmera</string>
	<key>CFBundleVersion</key>
	<string>${SHORT_VERSION}</string>
	<key>CFBundleShortVersionString</key>
	<string>${SHORT_VERSION}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
PLIST

# --- signing ---
sign_adhoc() {
	echo "ad-hoc signing (not notarized)"
	codesign --force --sign - --timestamp=none "$APP/Contents/Resources/charmera"
	codesign --force --sign - --timestamp=none "$APP/Contents/MacOS/Charmera"
	codesign --force --sign - --timestamp=none "$APP"
}

sign_developer_id() {
	local keychain="charmera-build.keychain" kpass="charmera-temp-pass"
	echo "$QUILL_SIGN_P12" | base64 --decode >"$DIST/cert.p12"
	security create-keychain -p "$kpass" "$keychain"
	security set-keychain-settings -lut 21600 "$keychain"
	security unlock-keychain -p "$kpass" "$keychain"
	security import "$DIST/cert.p12" -k "$keychain" -P "$QUILL_SIGN_PASSWORD" -T /usr/bin/codesign
	security set-key-partition-list -S apple-tool:,apple: -s -k "$kpass" "$keychain" >/dev/null
	security list-keychains -d user -s "$keychain" login.keychain
	rm -f "$DIST/cert.p12"

	local identity
	identity=$(security find-identity -v -p codesigning "$keychain" | awk '/Developer ID Application/ {print $2; exit}')
	if [ -z "$identity" ]; then
		echo "no Developer ID Application identity found in keychain" >&2
		exit 1
	fi

	echo "signing with Developer ID ($identity)"
	# Sign inside-out, with hardened runtime (required for notarization).
	codesign --force --options runtime --timestamp --sign "$identity" "$APP/Contents/Resources/charmera"
	codesign --force --options runtime --timestamp --sign "$identity" "$APP/Contents/MacOS/Charmera"
	codesign --force --options runtime --timestamp --sign "$identity" "$APP"
	codesign --verify --deep --strict --verbose=2 "$APP"
}

notarize() {
	echo "$QUILL_NOTARY_KEY" | base64 --decode >"$DIST/notary.p8"
	/usr/bin/ditto -c -k --keepParent "$APP" "$ZIP"
	echo "submitting to notary service…"
	xcrun notarytool submit "$ZIP" \
		--key "$DIST/notary.p8" \
		--key-id "$QUILL_NOTARY_KEY_ID" \
		--issuer "$QUILL_NOTARY_ISSUER" \
		--wait
	rm -f "$DIST/notary.p8"
	xcrun stapler staple "$APP"
	rm -f "$ZIP" # re-zip with the stapled ticket below
}

if [ -n "${QUILL_SIGN_P12:-}" ]; then
	sign_developer_id
	if [ -n "${QUILL_NOTARY_KEY:-}" ]; then
		notarize
	fi
else
	sign_adhoc
fi

/usr/bin/ditto -c -k --keepParent "$APP" "$ZIP"
echo "built $ZIP"
