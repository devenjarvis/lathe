#!/usr/bin/env bash
# Build, sign and notarize Lathe.app (macOS only).
#
#   VERSION=1.2.3 build/darwin/package.sh
#
# Produces $OUT_DIR/Lathe_<VERSION>_macos_universal.zip: a universal
# (arm64 + x86_64) Lathe.app whose executable is the single `lathe` binary built
# with `-tags desktop`. Run from the repo root on a Mac with Xcode command-line
# tools (clang, lipo, codesign, iconutil, sips, plutil, xcrun). Both arches build
# on either host arch: cmd/go passes `-arch arm64|x86_64` to clang itself for
# darwin cgo builds, so only GOARCH changes between the two builds.
#
# Inputs (environment):
#   VERSION            required; release version without the leading "v"
#   COMMIT, DATE       stamped into internal/buildinfo (default: git HEAD, now)
#   OUT_DIR            output directory (default: dist-macos)
#   ICON               1024x1024 PNG (default: internal/desktop/appicon.png)
#   REQUIRE_SIGNING    "true" makes missing signing/notary secrets an error
#                      instead of a warning (release.yml sets it for tag pushes
#                      on the upstream repo)
#
# Signing secrets (all five, or none):
#   MACOS_SIGN_P12          base64 of the "Developer ID Application" cert + key (.p12)
#   MACOS_SIGN_PASSWORD     password of that .p12
#   MACOS_NOTARY_KEY        base64 of the App Store Connect API key (.p8)
#   MACOS_NOTARY_KEY_ID     that key's ID
#   MACOS_NOTARY_ISSUER_ID  the App Store Connect issuer ID (UUID)
#
# With no secrets the app is ad-hoc signed (so it runs on Apple Silicon at all)
# and not notarized: fine for CI and dry runs, but Gatekeeper will refuse a
# downloaded copy. With secrets it is signed with the hardened runtime,
# notarized with `notarytool --wait`, stapled, and checked with spctl.
#
# Entitlements: none. Hardened-runtime entitlements are only needed for JIT,
# unsigned executable memory, DYLD_* variables or loading third-party dylibs.
# Go does none of these, and WKWebView runs its JavaScript JIT in Apple's own
# WebContent XPC processes, which carry their own entitlements. The app is not
# sandboxed, so no network/file entitlements either. Child processes (the
# user's ACP agent, e.g. node) are separate executables with their own
# signatures; the hardened runtime is not inherited. If a future feature needs
# one, add build/darwin/entitlements.plist and pass --entitlements below.
set -euo pipefail

MIN_MACOS=12.0 # keep in sync with LSMinimumSystemVersion in Info.plist
APP_NAME=Lathe
BIN=lathe
BUILDINFO=github.com/devenjarvis/lathe/internal/buildinfo

log() { printf '==> %s\n' "$*"; }
die() {
	printf '::error::%s\n' "$*" >&2
	exit 1
}

[ "$(uname -s)" = Darwin ] || die "package.sh must run on macOS"
: "${VERSION:?set VERSION, e.g. VERSION=1.2.3}"
VERSION=${VERSION#v}
COMMIT=${COMMIT:-$(git rev-parse HEAD)}
DATE=${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
OUT_DIR=${OUT_DIR:-dist-macos}
ICON=${ICON:-internal/desktop/appicon.png}
REQUIRE_SIGNING=${REQUIRE_SIGNING:-false}

# CFBundleShortVersionString/CFBundleVersion must be numeric: 1.2.0-rc.1 -> 1.2.0.
SHORT_VERSION=$(printf '%s' "$VERSION" | sed -E 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/')
case "$SHORT_VERSION" in
[0-9]*) ;;
*) SHORT_VERSION=0.0.0 ;;
esac

[ -f "$ICON" ] || die "app icon not found: $ICON"

# --- decide whether to sign -------------------------------------------------
secrets=(MACOS_SIGN_P12 MACOS_SIGN_PASSWORD MACOS_NOTARY_KEY MACOS_NOTARY_KEY_ID MACOS_NOTARY_ISSUER_ID)
set_count=0
missing=()
for s in "${secrets[@]}"; do
	if [ -n "${!s:-}" ]; then
		set_count=$((set_count + 1))
	else
		missing+=("$s")
	fi
done
if [ "$set_count" -eq "${#secrets[@]}" ]; then
	SIGN=true
elif [ "$set_count" -eq 0 ] && [ "$REQUIRE_SIGNING" != true ]; then
	SIGN=false
	printf '::warning::macOS signing secrets not set: Lathe.app will be ad-hoc signed and NOT notarized (fine for forks, CI and dry runs).\n'
else
	die "macOS signing is required or partially configured, but these secrets are missing: ${missing[*]}"
fi

work=$(mktemp -d)
KEYCHAIN=""
cleanup() {
	if [ -n "$KEYCHAIN" ]; then
		security delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

# --- build both arches and merge -------------------------------------------
ldflags="-s -w -X $BUILDINFO.Version=$VERSION -X $BUILDINFO.Commit=$COMMIT -X $BUILDINFO.Date=$DATE"
for arch in arm64 amd64; do
	log "go build -tags desktop,production (darwin/$arch)"
	CGO_ENABLED=1 GOOS=darwin GOARCH=$arch MACOSX_DEPLOYMENT_TARGET=$MIN_MACOS \
		go build -tags desktop,production -ldflags "$ldflags" -o "$work/$BIN-$arch" .
done
lipo -create -output "$work/$BIN" "$work/$BIN-arm64" "$work/$BIN-amd64"
log "universal binary: $(lipo -archs "$work/$BIN")"

# --- assemble the bundle ----------------------------------------------------
app="$work/$APP_NAME.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
install -m 0755 "$work/$BIN" "$app/Contents/MacOS/$BIN"
sed -e "s/@SHORT_VERSION@/$SHORT_VERSION/g" -e "s/@VERSION@/$VERSION/g" \
	build/darwin/Info.plist >"$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist"
printf 'APPL????' >"$app/Contents/PkgInfo"

log "generating icons.icns from $ICON"
iconset="$work/icons.iconset"
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$ICON" --out "$iconset/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z "$double" "$double" "$ICON" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns -o "$app/Contents/Resources/icons.icns" "$iconset"

# --- sign -------------------------------------------------------------------
if [ "$SIGN" = true ]; then
	log "importing the Developer ID certificate into a temporary keychain"
	KEYCHAIN="$work/lathe-signing.keychain-db"
	kc_pass=$(openssl rand -hex 24)
	printf '%s' "$MACOS_SIGN_P12" | base64 --decode >"$work/cert.p12"
	security create-keychain -p "$kc_pass" "$KEYCHAIN"
	security set-keychain-settings -lut 3600 "$KEYCHAIN"
	security unlock-keychain -p "$kc_pass" "$KEYCHAIN"
	security import "$work/cert.p12" -k "$KEYCHAIN" -P "$MACOS_SIGN_PASSWORD" \
		-f pkcs12 -T /usr/bin/codesign >/dev/null
	rm -f "$work/cert.p12"
	security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$kc_pass" "$KEYCHAIN" >/dev/null
	# codesign --timestamp and notarization look the cert chain up via the search list.
	existing=$(security list-keychains -d user | tr -d '"' | xargs)
	# shellcheck disable=SC2086 # word-splitting the existing list is intended
	security list-keychains -d user -s "$KEYCHAIN" $existing
	identity=$(security find-identity -v -p codesigning "$KEYCHAIN" |
		awk '/"Developer ID Application: / {print $2; exit}')
	[ -n "$identity" ] || die "no 'Developer ID Application' identity in MACOS_SIGN_P12"

	log "codesign (hardened runtime) with $identity"
	codesign --force --options runtime --timestamp \
		--keychain "$KEYCHAIN" --sign "$identity" "$app"
else
	log "codesign (ad-hoc)"
	codesign --force --sign - "$app"
fi
codesign --verify --strict --verbose=2 "$app"

# --- smoke test: the bundled binary still works as the CLI -------------------
"$app/Contents/MacOS/$BIN" --version
if arch -x86_64 /usr/bin/true 2>/dev/null; then
	arch -x86_64 "$app/Contents/MacOS/$BIN" --version
fi

# --- notarize + staple ------------------------------------------------------
mkdir -p "$OUT_DIR"
zip="$OUT_DIR/${APP_NAME}_${VERSION}_macos_universal.zip"
rm -f "$zip"
if [ "$SIGN" = true ]; then
	log "notarizing (xcrun notarytool submit --wait)"
	printf '%s' "$MACOS_NOTARY_KEY" | base64 --decode >"$work/notary.p8"
	ditto -c -k --sequesterRsrc --keepParent "$app" "$work/submit.zip"
	xcrun notarytool submit "$work/submit.zip" \
		--key "$work/notary.p8" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" \
		--wait --timeout 30m --output-format json >"$work/notary.json" || true
	cat "$work/notary.json"
	status=$(plutil -extract status raw -o - "$work/notary.json" 2>/dev/null || echo unknown)
	if [ "$status" != Accepted ]; then
		if id=$(plutil -extract id raw -o - "$work/notary.json" 2>/dev/null); then
			xcrun notarytool log "$id" --key "$work/notary.p8" \
				--key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" || true
		fi
		rm -f "$work/notary.p8"
		die "notarization failed: status=$status"
	fi
	rm -f "$work/notary.p8"
	xcrun stapler staple "$app"
	xcrun stapler validate "$app"
	spctl --assess --type execute --verbose=4 "$app"
fi

ditto -c -k --sequesterRsrc --keepParent "$app" "$zip"
log "wrote $zip ($(if [ "$SIGN" = true ]; then echo "signed + notarized"; else echo "ad-hoc signed, not notarized"; fi))"
