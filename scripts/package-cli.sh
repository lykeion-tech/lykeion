#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  printf 'Usage: %s VERSION OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
version=${1#v}
case "$version" in ''|*[!a-zA-Z0-9._-]*) printf 'Invalid release version\n' >&2; exit 1 ;; esac
root=$(cd "$(dirname "$0")/.." && pwd)
commit=$(git -C "$root" rev-parse --verify HEAD)
verify_source() {
  [ -z "$(git -C "$root" status --porcelain --untracked-files=all)" ] || { printf 'Release packaging requires a clean checkout\n' >&2; return 1; }
  [ "$(git -C "$root" rev-parse --verify HEAD)" = "$commit" ] || { printf 'Source revision changed during packaging\n' >&2; return 1; }
  [ "$(git -C "$root" rev-parse --verify "refs/tags/v$version^{commit}")" = "$commit" ] || { printf 'Release tag must point to HEAD\n' >&2; return 1; }
}
verify_source
mkdir -p "$2"
out=$(cd "$2" && pwd)
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  [ ! -e "$out/lyk-cli-$version-$target.tar.gz" ] || { printf 'Release artifact already exists\n' >&2; exit 1; }
done
[ ! -e "$out/SHA256SUMS" ] || { printf 'Release checksums already exist\n' >&2; exit 1; }
staging=$(mktemp -d "${TMPDIR:-/tmp}/lyk-release.XXXXXX")
trap 'rm -rf "$staging"' EXIT
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  os=${target%-*}; arch=${target#*-}
  (cd "$root/server" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=true -o "$staging/lyk" ./cmd/lyk)
  info=$(go version -m "$staging/lyk" | sed 's/^[[:space:]]*//')
  for expected in "$(printf 'build\tGOOS=%s' "$os")" "$(printf 'build\tGOARCH=%s' "$arch")" "$(printf 'build\tvcs.revision=%s' "$commit")" "$(printf 'build\tvcs.modified=false')"; do
    printf '%s\n' "$info" | grep -Fx "$expected" >/dev/null || { printf 'Built CLI metadata does not match release source and target\n' >&2; exit 1; }
  done
  chmod 755 "$staging/lyk"
  tar -czf "$staging/lyk-cli-$version-$target.tar.gz" -C "$staging" lyk
done
rm "$staging/lyk"
: > "$staging/SHA256SUMS"
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  name="lyk-cli-$version-$target.tar.gz"
  if command -v sha256sum >/dev/null 2>&1; then
    digest=$(sha256sum "$staging/$name")
  elif command -v shasum >/dev/null 2>&1; then
    digest=$(shasum -a 256 "$staging/$name")
  else
    printf 'Install sha256sum or shasum to build release checksums\n' >&2
    exit 1
  fi
  printf '%s  %s\n' "${digest%% *}" "$name" >> "$staging/SHA256SUMS"
done
verify_source
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  name="lyk-cli-$version-$target.tar.gz"
  mv "$staging/$name" "$out/$name"
done
mv "$staging/SHA256SUMS" "$out/SHA256SUMS"
printf 'CLI release artifacts ready in %s\n' "$out"
