#!/usr/bin/env bash
# Sign (and, when Developer ID credentials are present, notarize) a macOS binary
# with quill. Invoked by GoReleaser's universal_binaries post hook.
#
# Degrades gracefully so a release never breaks before signing is configured:
#   - quill not installed        -> skip (e.g. local `goreleaser --snapshot`)
#   - no QUILL_SIGN_P12 in env    -> ad-hoc sign only (not notarized)
#   - credentials present         -> Developer ID sign + notarize
#
# Credentials (set as GitHub Actions secrets): QUILL_SIGN_P12 (base64 .p12),
# QUILL_SIGN_PASSWORD, QUILL_NOTARY_KEY (base64 .p8), QUILL_NOTARY_KEY_ID,
# QUILL_NOTARY_ISSUER.
set -euo pipefail

bin="${1:?usage: macos-sign.sh <binary>}"

if ! command -v quill >/dev/null 2>&1; then
	echo "quill not installed — skipping macOS code signing for $bin"
	exit 0
fi

if [ -z "${QUILL_SIGN_P12:-}" ]; then
	echo "no Developer ID credentials — ad-hoc signing $bin (not notarized)"
	quill sign --ad-hoc "$bin" || echo "ad-hoc signing failed; leaving $bin unsigned"
	exit 0
fi

echo "signing and notarizing $bin"
quill sign-and-notarize "$bin"
