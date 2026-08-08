#!/bin/sh
# mdfly installer.
#
#   curl -fsSL https://mdfly.dev/install.sh | sh
#
# The server embeds this file and serves it at that URL; this copy is the only
# one, so editing it here changes what users pipe into sh on the next deploy.
#
# Overrides:
#   MDFLY_VERSION      tag to install, with or without the leading v (default: latest)
#   MDFLY_INSTALL_DIR  destination directory (default: /usr/local/bin, else ~/.local/bin)
#
# POSIX sh on purpose: this runs on whatever /bin/sh the machine has, which on
# Debian derivatives is dash, so no bashisms.

set -eu

REPO="Abir66/mdfly"
BINARY="mdfly"
DEFAULT_DIR="/usr/local/bin"
FALLBACK_DIR="$HOME/.local/bin"

die() {
	echo "install: $*" >&2
	exit 1
}

info() {
	echo "install: $*" >&2
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

# Maps uname output onto the goos/goarch pair goreleaser named the archive with.
detect_platform() {
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	case "$os" in
	linux | darwin) ;;
	*) die "unsupported OS: $os. Windows users: download the zip from https://github.com/$REPO/releases" ;;
	esac

	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
	esac

	PLATFORM="${os}_${arch}"
}

# Resolves the tag from the GitHub API rather than a "latest" download alias,
# because the archive filename embeds the version and must be known up front.
resolve_version() {
	if [ -n "${MDFLY_VERSION:-}" ]; then
		VERSION=${MDFLY_VERSION#v}
		return
	fi

	tag=$(download_stdout "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
		head -n 1)
	[ -n "$tag" ] || die "could not resolve the latest release; set MDFLY_VERSION to install a specific tag"
	VERSION=${tag#v}
}

download_stdout() {
	if [ "$FETCHER" = curl ]; then
		curl -fsSL "$1"
	else
		wget -qO- "$1"
	fi
}

download_file() {
	if [ "$FETCHER" = curl ]; then
		curl -fsSL -o "$2" "$1"
	else
		wget -qO "$2" "$1"
	fi
}

# Refuses to install an archive whose digest is absent from the release's
# checksums.txt — a truncated or substituted download must fail loudly.
verify_checksum() {
	archive=$1
	sums=$2

	expected=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$sums" | head -n 1)
	[ -n "$expected" ] || die "$archive is not listed in checksums.txt"

	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$archive" | awk '{ print $1 }')
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$archive" | awk '{ print $1 }')
	else
		die "no sha256sum or shasum available to verify the download"
	fi

	[ "$actual" = "$expected" ] || die "checksum mismatch for $archive (expected $expected, got $actual)"
}

# Prefers a system-wide install, falls back to the per-user bin rather than
# escalating to sudo on its own.
choose_install_dir() {
	if [ -n "${MDFLY_INSTALL_DIR:-}" ]; then
		INSTALL_DIR=$MDFLY_INSTALL_DIR
		mkdir -p "$INSTALL_DIR" || die "cannot create $INSTALL_DIR"
		return
	fi

	if [ -w "$DEFAULT_DIR" ]; then
		INSTALL_DIR=$DEFAULT_DIR
	else
		INSTALL_DIR=$FALLBACK_DIR
		mkdir -p "$INSTALL_DIR"
	fi
}

warn_if_not_on_path() {
	case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		info ""
		info "$INSTALL_DIR is not on your PATH. Add this to your shell profile:"
		info "    export PATH=\"\$PATH:$INSTALL_DIR\""
		;;
	esac
}

main() {
	if command -v curl >/dev/null 2>&1; then
		FETCHER=curl
	elif command -v wget >/dev/null 2>&1; then
		FETCHER=wget
	else
		die "need curl or wget"
	fi
	need tar
	need awk

	detect_platform
	resolve_version
	choose_install_dir

	archive="${BINARY}_${VERSION}_${PLATFORM}.tar.gz"
	base="https://github.com/$REPO/releases/download/v${VERSION}"

	tmp=$(mktemp -d)
	# A signal trap that only cleans up would return to the next command with the
	# temp dir already gone, so an interrupted install carries on and can still
	# exit 0. The signal handlers therefore exit themselves, with the usual
	# 128+signo status.
	trap 'rm -rf "$tmp"' EXIT
	trap 'rm -rf "$tmp"; exit 130' INT
	trap 'rm -rf "$tmp"; exit 143' TERM

	info "downloading $BINARY v$VERSION ($PLATFORM)"
	download_file "$base/$archive" "$tmp/$archive" ||
		die "download failed: $base/$archive"
	download_file "$base/checksums.txt" "$tmp/checksums.txt" ||
		die "could not fetch checksums.txt for v$VERSION"

	( cd "$tmp" && verify_checksum "$archive" checksums.txt )
	tar -xzf "$tmp/$archive" -C "$tmp"
	[ -f "$tmp/$BINARY" ] || die "archive did not contain a $BINARY binary"

	install -m 0755 "$tmp/$BINARY" "$INSTALL_DIR/$BINARY" 2>/dev/null ||
		die "cannot write to $INSTALL_DIR. Re-run with MDFLY_INSTALL_DIR=<dir>, or use sudo."

	info "installed $INSTALL_DIR/$BINARY (v$VERSION)"
	warn_if_not_on_path
}

main "$@"
