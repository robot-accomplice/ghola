#!/usr/bin/env sh
set -eu

REPO="robot-accomplice/ghola"
API_URL="https://api.github.com/repos/$REPO/releases/latest"
INSTALL_DIR="${GHOLA_INSTALL_DIR:-$HOME/.local/bin}"

need_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "Missing required command: $1" >&2
        exit 1
    fi
}

need_cmd curl
need_cmd tar
need_cmd uname

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch_raw="$(uname -m)"

case "$os" in
    linux|darwin) ;;
    *)
        echo "Unsupported OS: $os (expected linux or darwin)" >&2
        exit 1
        ;;
esac

case "$arch_raw" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *)
        echo "Unsupported architecture: $arch_raw (expected amd64 or arm64)" >&2
        exit 1
        ;;
esac

tag="$(
    curl -fsSL "$API_URL" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n1
)"

if [ -z "$tag" ]; then
    echo "Failed to determine latest release tag from GitHub API." >&2
    exit 1
fi

# Release assets carry the version without the tag's leading "v".
version="${tag#v}"
asset="ghola_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/download/$tag"
download_url="$base_url/$asset"

if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
    echo "Missing required command: sha256sum or shasum" >&2
    exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() {
    rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

echo "Downloading $asset..."
curl -fL "$download_url" -o "$tmpdir/$asset"

curl -fsSL "$base_url/checksums.txt" -o "$tmpdir/checksums.txt"
want="$(awk -v a="$asset" '$2 == a { print $1; exit }' "$tmpdir/checksums.txt")"
if [ -z "$want" ]; then
    echo "checksums.txt of $tag has no entry for $asset." >&2
    exit 1
fi
got="$(sha256 "$tmpdir/$asset")"
if [ "$got" != "$want" ]; then
    echo "Checksum mismatch for $asset: expected $want, got $got." >&2
    exit 1
fi
echo "Checksum OK."

echo "Extracting..."
tar -xzf "$tmpdir/$asset" -C "$tmpdir"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmpdir/ghola" "$INSTALL_DIR/ghola"

echo "Installed ghola to $INSTALL_DIR/ghola"
if command -v ghola >/dev/null 2>&1; then
    echo "ghola is now available on PATH."
else
    echo "Add this directory to your PATH if needed:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

echo "Verify with: ghola --version"
