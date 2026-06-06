#!/bin/sh
# Lathe installer.
#
#   curl -sSf https://raw.githubusercontent.com/devenjarvis/lathe/main/install.sh | sh
#
# Detects your OS/arch, downloads the matching release archive from GitHub,
# verifies its checksum, and installs the `lathe` binary onto your PATH.
#
# Environment overrides:
#   LATHE_VERSION      install a specific tag (e.g. v0.1.0) instead of latest
#   LATHE_INSTALL_DIR  install into this dir instead of the default
#
# After installing, run `lathe skills install` to set up the Claude Code / Cursor
# skills in your project.
set -eu

REPO="devenjarvis/lathe"
BIN="lathe"

info() { printf '%s\n' "$*"; }
err() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

need() { command -v "$1" >/dev/null 2>&1 || err "required tool not found: $1"; }

need uname
need curl
need tar
need mktemp

# --- detect platform ---------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
darwin) os=darwin ;;
linux) os=linux ;;
*) err "unsupported OS: $os (lathe ships darwin and linux builds)" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) err "unsupported architecture: $arch" ;;
esac

# --- pick a sha256 tool ------------------------------------------------------
if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
	err "need sha256sum or shasum to verify the download"
fi

# --- resolve version ---------------------------------------------------------
version="${LATHE_VERSION:-}"
if [ -z "$version" ]; then
	info "Resolving latest release..."
	version=$(curl -sSfL "https://api.github.com/repos/$REPO/releases/latest" |
		grep '"tag_name"' | head -1 |
		sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
fi
[ -n "$version" ] || err "could not determine the release version"

# GoReleaser strips the leading "v" from archive names; the tag keeps it.
num=${version#v}
archive="${BIN}_${num}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

# --- download + verify -------------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

info "Downloading $archive ($version)..."
curl -sSfL "$base/$archive" -o "$tmp/$archive" || err "download failed: $base/$archive"
curl -sSfL "$base/checksums.txt" -o "$tmp/checksums.txt" || err "could not fetch checksums.txt"

expected=$(grep " ${archive}\$" "$tmp/checksums.txt" | awk '{print $1}')
[ -n "$expected" ] || err "no checksum found for $archive"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || err "checksum mismatch for $archive (expected $expected, got $actual)"
info "Checksum verified."

tar -xzf "$tmp/$archive" -C "$tmp" "$BIN" || err "could not extract $BIN from archive"

# --- choose an install dir ---------------------------------------------------
install_dir="${LATHE_INSTALL_DIR:-}"
use_sudo=""
if [ -z "$install_dir" ]; then
	install_dir="$HOME/.local/bin"
	# If ~/.local/bin can't be used but /usr/local/bin exists, fall back to it
	# (with sudo when needed).
	if ! mkdir -p "$install_dir" 2>/dev/null; then
		install_dir="/usr/local/bin"
	fi
fi

mkdir -p "$install_dir" 2>/dev/null || true
if [ ! -w "$install_dir" ]; then
	if command -v sudo >/dev/null 2>&1; then
		use_sudo="sudo"
		info "Installing to $install_dir (needs sudo)..."
	else
		err "$install_dir is not writable and sudo is unavailable; set LATHE_INSTALL_DIR to a writable dir"
	fi
fi

$use_sudo install -m 0755 "$tmp/$BIN" "$install_dir/$BIN" ||
	err "failed to install into $install_dir"

info "Installed $BIN to $install_dir/$BIN"
"$install_dir/$BIN" --version || true

# --- PATH hint ---------------------------------------------------------------
case ":$PATH:" in
*":$install_dir:"*) ;;
*)
	info ""
	info "Note: $install_dir is not on your PATH. Add it, e.g.:"
	info "  echo 'export PATH=\"$install_dir:\$PATH\"' >> ~/.profile && . ~/.profile"
	;;
esac

info ""
info "Next: cd into your project and run \`lathe skills install\` to install the"
info "Claude Code / Cursor skills (add --agent cursor or --user as needed)."
