#!/bin/sh
# Herdr runs this as the plugin's build step. It puts the quartermaster binary
# at bin/quartermaster (with bin/qm, its short alias, and bin/flatcircle,
# bin/kelpie and bin/shepherd, the deprecated old names, linked to it): the
# prebuilt release named by herdr-plugin.toml's version, checked against the
# release's SHA256SUMS, or `go build` when there is no such release, it can't
# be verified, or this checkout isn't that release.
#
#   QUARTERMASTER_BUILD=source   always build from source (FLATCIRCLE_BUILD,
#                                KELPIE_BUILD and SHEPHERD_BUILD still work)
set -u
cd "$(dirname "$0")/.." || exit 1

say() { printf 'quartermaster build: %s\n' "$*" >&2; }

from_source() {
  say "$1; building from source"
  command -v go >/dev/null 2>&1 || { say "go is not installed; install Go 1.26+ or use a tagged release"; exit 1; }
  mkdir -p bin && go build -o bin/quartermaster . || exit 1
  alias_old_names
  exit 0
}

# qm is the short alias. flatcircle, kelpie and shepherd are old names; links
# to them (~/.local/bin/flatcircle) keep working through these, and
# quartermaster prints a deprecation notice when run by one.
alias_old_names() {
  for alias in qm flatcircle kelpie shepherd; do ln -sfn quartermaster "bin/$alias"; done
}

[ "${QUARTERMASTER_BUILD:-${FLATCIRCLE_BUILD:-${KELPIE_BUILD:-${SHEPHERD_BUILD:-}}}}" = source ] && from_source "QUARTERMASTER_BUILD=source"

version=$(sed -n 's/^version *= *"\([^"]*\)".*/\1/p' herdr-plugin.toml | head -n 1)
[ -n "$version" ] || from_source "herdr-plugin.toml has no version"
tag="v$version"

# A prebuilt binary is only the code of the release commit itself; a checkout
# with changes, or on another commit, builds what it has.
if [ -d .git ] || [ -f .git ]; then
  [ -z "$(git status --porcelain --untracked-files=no 2>/dev/null)" ] || from_source "the checkout has uncommitted changes"
  head=$(git rev-parse HEAD 2>/dev/null)
  release=$(git rev-parse -q --verify "refs/tags/$tag^{commit}" 2>/dev/null ||
    GIT_TERMINAL_PROMPT=0 git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}" 2>/dev/null |
      awk '{ sha = $1 } $2 ~ /\^\{\}$/ { peeled = $1 } END { print (peeled != "" ? peeled : sha) }')
  if [ "$head" != "$release" ]; then
    # Without Go, the manifest's release binary beats a failed install.
    command -v go >/dev/null 2>&1 && from_source "this checkout is not the $tag release commit"
    say "this checkout is not the $tag release commit and go is not installed; using the $tag binary"
  fi
fi

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) from_source "no prebuilt binary for $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) from_source "no prebuilt binary for $(uname -m)" ;;
esac
asset="quartermaster-$os-$arch"
base="https://github.com/travisjeffery/herdr-quartermaster/releases/download/$tag"

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 2 --connect-timeout 10 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  from_source "neither curl nor wget is installed"
fi
if command -v sha256sum >/dev/null 2>&1; then
  sum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  from_source "no sha256sum or shasum to verify the download"
fi

tmp=$(mktemp -d) || from_source "mktemp failed"
trap 'rm -rf "$tmp"' EXIT
say "downloading $asset $tag"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || from_source "could not download $base/SHA256SUMS"
fetch "$base/$asset" "$tmp/$asset" || from_source "could not download $base/$asset"
want=$(awk -v f="$asset" '$2 == f {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || from_source "SHA256SUMS has no entry for $asset"
[ "$(sum "$tmp/$asset")" = "$want" ] || from_source "$asset does not match SHA256SUMS"

mkdir -p bin && chmod +x "$tmp/$asset" && mv "$tmp/$asset" bin/quartermaster
alias_old_names
say "installed $asset $tag"
