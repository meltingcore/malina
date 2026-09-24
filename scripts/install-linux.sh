#!/bin/sh
set -eu

REPOSITORY="meltingcore/malina"
RELEASES_API="https://api.github.com/repos/${REPOSITORY}/releases/latest"

fail() {
  printf 'malina installer: %s\n' "$1" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v cp >/dev/null 2>&1 || fail "cp is required"
[ "$(uname -s)" = Linux ] || fail "this installer supports Linux only"

case "$(uname -m)" in
  x86_64|amd64) architecture=amd64 ;;
  aarch64|arm64) architecture=arm64 ;;
  *) fail "unsupported Linux architecture: $(uname -m)" ;;
esac

if [ -n "${MALINA_VERSION:-}" ]; then
  version=${MALINA_VERSION#v}
else
  release_json=$(curl -fsSL "$RELEASES_API") || fail "could not fetch latest release metadata"
  version=$(printf '%s\n' "$release_json" | sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"v\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$version" ] || fail "could not find a stable release tag"
fi

printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "invalid version: $version"

archive_name="malina-${version}-linux-${architecture}.tar.gz"
archive_url="https://github.com/${REPOSITORY}/releases/download/v${version}/${archive_name}"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/malina-install.XXXXXX") || fail "could not create a temporary directory"
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

printf 'Downloading Malina %s for Linux %s...\n' "$version" "$architecture"
curl -fL --retry 3 --retry-delay 1 "$archive_url" -o "$temporary_dir/$archive_name" || fail "could not download $archive_name"
tar -xzf "$temporary_dir/$archive_name" -C "$temporary_dir" || fail "could not extract release archive"

release_dir="$temporary_dir/malina-${version}-linux-${architecture}"

if [ "$(id -u)" -eq 0 ] || { [ -w /usr/local/bin ] && [ -w /usr/local/share ]; }; then
  cp -R "$release_dir/." /usr/local/
else
  command -v sudo >/dev/null 2>&1 || fail "sudo is required to install to /usr/local"
  sudo cp -R "$release_dir/." /usr/local/
fi

printf 'Installed Malina %s \n' "$version"
