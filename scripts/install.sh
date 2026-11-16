#!/usr/bin/env bash
set -euo pipefail

fail() { printf 'Lykeion installer: %s\n' "$*" >&2; exit 1; }
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "Only macOS and Linux are supported." ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "This machine's architecture is not supported." ;;
esac

repo=https://github.com/lykeion-tech/lykeion
release_url=$(curl -fsS -o /dev/null -w '%{redirect_url}' "$repo/releases/latest") || fail 'Could not determine the latest release.'
case "$release_url" in
  "$repo"/releases/tag/*) tag=${release_url#"$repo"/releases/tag/} ;;
  *) fail 'Could not determine the latest release.' ;;
esac
case "$tag" in
  ''|*[!a-zA-Z0-9._-]*) fail 'Invalid release tag.' ;;
esac
version=${tag#v}
[ -n "$version" ] || fail 'Invalid release tag.'
archive="lyk-cli-${version}-${os}-${arch}.tar.gz"
tmp=$(mktemp -d) || fail 'Could not create a temporary directory.'
staged=''
cleanup() {
  rm -rf "$tmp"
  if [ -n "$staged" ]; then
    if [ "${use_sudo:-false}" = true ]; then sudo -n rm -f "$staged"; else rm -f "$staged"; fi
  fi
}
trap cleanup EXIT

curl -fsSL -o "$tmp/$archive" "$repo/releases/download/$tag/$archive" || fail 'Could not download the CLI for this machine.'
curl -fsSL -o "$tmp/SHA256SUMS" "$repo/releases/download/$tag/SHA256SUMS" || fail 'Could not download release checksums.'
expected=$(awk -v name="$archive" '$2 == name {print $1; count++} END {if (count != 1) exit 1}' "$tmp/SHA256SUMS") || fail 'Release checksum is missing or ambiguous.'
case "$expected" in *[!0-9a-fA-F]*|'') fail 'Invalid release checksum.' ;; esac
[ "${#expected}" = 64 ] || fail 'Invalid release checksum.'
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive")
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$archive")
else
  fail 'Install sha256sum or shasum to verify the download.'
fi
actual=${actual%% *}
[ "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" = "$(printf '%s' "$actual" | tr 'A-F' 'a-f')" ] || fail 'Release checksum did not match; the existing installation was not changed.'
entries=$(tar -tzf "$tmp/$archive") || fail 'Could not read the CLI archive.'
[ "$entries" = lyk ] || fail 'The CLI archive has unexpected contents.'
tar -xOzf "$tmp/$archive" lyk > "$tmp/lyk" || fail 'Could not extract the CLI.'
[ -s "$tmp/lyk" ] || fail 'The CLI archive is empty.'

use_sudo=false
if [ -n "${LYK_BIN_DIR:-}" ]; then
  bin_dir=$LYK_BIN_DIR
  mkdir -p "$bin_dir" || fail 'Could not create the installation directory.'
elif [ -w /usr/local/bin ]; then
  bin_dir=/usr/local/bin
elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
  bin_dir=/usr/local/bin
  use_sudo=true
else
  bin_dir="$HOME/.local/bin"
  mkdir -p "$bin_dir" || fail 'Could not create the installation directory.'
fi
[ ! -d "$bin_dir/lyk" ] || fail 'The installation path is a directory.'
if [ "$use_sudo" = true ]; then
  staged=$(sudo -n mktemp "$bin_dir/.lyk-install.XXXXXX") || fail 'Could not prepare the installation.'
  sudo -n install -m 755 "$tmp/lyk" "$staged" || fail 'Could not prepare the CLI.'
  [ ! -d "$bin_dir/lyk" ] || fail 'The installation path is a directory.'
  sudo -n mv -f "$staged" "$bin_dir/lyk" || fail 'Could not install the CLI.'
else
  staged=$(mktemp "$bin_dir/.lyk-install.XXXXXX") || fail 'Could not prepare the installation.'
  install -m 755 "$tmp/lyk" "$staged" || fail 'Could not prepare the CLI.'
  [ ! -d "$bin_dir/lyk" ] || fail 'The installation path is a directory.'
  mv -f "$staged" "$bin_dir/lyk" || fail 'Could not install the CLI.'
fi
staged=''
printf 'Lykeion CLI installed at %s/lyk\n' "$bin_dir"
active=$(command -v lyk || true)
if [ -n "$active" ] && [ ! "$active" -ef "$bin_dir/lyk" ]; then
  printf 'Another lyk at %s is earlier on PATH. Put %s first on PATH to use this installation.\n' "$active" "$bin_dir"
else
  case ":$PATH:" in *":$bin_dir:"*) ;; *) printf 'Add %s to your PATH to run lyk.\n' "$bin_dir" ;; esac
fi
printf 'Next: %s/lyk login --email you@lab.org --server https://app.lykeion.tech\n' "$bin_dir"
