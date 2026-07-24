#!/usr/bin/env sh
# Install compound from a prebuilt release tarball.
# Default: download from GitHub releases for umutkeltek/compound.
# Offline:  COMPOUND_RELEASE_TARBALL=/path/to/compound_<ver>_<os>_<arch>.tar.gz
set -eu

die() {
  printf 'install-release: %s\n' "$1" >&2
  exit 1
}

# version baked into this installer; override with COMPOUND_VERSION env
COMPOUND_VERSION="${COMPOUND_VERSION:-0.1.0}"
# base URL for downloads; override with COMPOUND_RELEASE_URL env
COMPOUND_RELEASE_URL_DEFAULT="https://github.com/umutkeltek/compound/releases/latest/download/"
COMPOUND_RELEASE_URL="${COMPOUND_RELEASE_URL:-$COMPOUND_RELEASE_URL_DEFAULT}"

# detect OS/arch
os=$(uname -s)
arch=$(uname -m)

case "$os" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *)      die "unsupported OS: $os (supported: darwin, linux)" ;;
esac

case "$arch" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *)             die "unsupported arch: $arch (supported: arm64, amd64)" ;;
esac

tarball="compound_${COMPOUND_VERSION}_${os}_${arch}.tar.gz"

# temp workdir; cleaned on exit
work_dir=$(mktemp -d 2>/dev/null || mktemp -d -t 'compound-install')
trap 'rm -rf "$work_dir"' EXIT INT TERM

# fetch tarball (local copy or download)
if [ -n "${COMPOUND_RELEASE_TARBALL:-}" ]; then
  if [ ! -f "$COMPOUND_RELEASE_TARBALL" ]; then
    die "tarball not found: $COMPOUND_RELEASE_TARBALL"
  fi
  cp "$COMPOUND_RELEASE_TARBALL" "$work_dir/$tarball"
  # also pick up co-located SHA256SUMS if present (offline verification)
  co_sums="$(dirname "$COMPOUND_RELEASE_TARBALL")/SHA256SUMS"
  if [ -f "$co_sums" ]; then
    cp "$co_sums" "$work_dir/SHA256SUMS"
  fi
else
  if ! command -v curl >/dev/null 2>&1; then
    die "curl is required for downloads (or set COMPOUND_RELEASE_TARBALL)"
  fi
  if ! curl -fsSL -o "$work_dir/$tarball" "$COMPOUND_RELEASE_URL$tarball"; then
    die "download failed: $COMPOUND_RELEASE_URL$tarball"
  fi
  # SHA256SUMS is optional; if missing, skip verification
  if curl -fsSL -o "$work_dir/SHA256SUMS" "$COMPOUND_RELEASE_URL"SHA256SUMS 2>/dev/null; then
    :
  else
    rm -f "$work_dir/SHA256SUMS"
  fi
fi

# verify checksum (when SHA256SUMS is present)
# SHA256SUMS may list other platforms; manually check only the tarball we have.
if [ -f "$work_dir/SHA256SUMS" ]; then
  expected=$(grep -E "^[0-9a-fA-F]+[[:space:]]+[*]?${tarball}\$" "$work_dir/SHA256SUMS" | head -n 1 | awk '{print $1}')
  if [ -z "$expected" ]; then
    die "no checksum entry for $tarball in SHA256SUMS"
  fi
  actual=$(cd "$work_dir" && shasum -a 256 "$tarball" | awk '{print $1}')
  if [ "$expected" != "$actual" ]; then
    die "checksum mismatch for $tarball: expected $expected, got $actual"
  fi
fi

# extract
if ! tar -xzf "$work_dir/$tarball" -C "$work_dir"; then
  die "extract failed: $work_dir/$tarball"
fi
bin_dir="$work_dir/compound_${COMPOUND_VERSION}_${os}_${arch}"
if [ ! -x "$bin_dir/compound" ] || [ ! -x "$bin_dir/compound-breadth-engine" ]; then
  die "tarball missing compound or compound-breadth-engine binaries"
fi

# install binaries
if [ -n "${GOBIN:-}" ]; then
  install_dir=$GOBIN
else
  if [ -z "${HOME:-}" ]; then
    die "HOME is required when GOBIN is not set"
  fi
  install_dir=$HOME/.local/bin
fi
mkdir -p "$install_dir"
cp "$bin_dir/compound"               "$install_dir/compound"
cp "$bin_dir/compound-breadth-engine" "$install_dir/compound-breadth-engine"

# resolve home
if [ -n "${COMPOUND_HOME:-}" ]; then
  compound_home=$COMPOUND_HOME
elif [ -n "${HOME:-}" ]; then
  compound_home=$HOME/.compound
else
  die "COMPOUND_HOME or HOME is required"
fi

# init + doctor (mirror install.sh post-install steps)
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" \
  "$install_dir/compound" home init --home "$compound_home" --global

PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" \
  "$install_dir/compound" home doctor --home "$compound_home"

cat <<EOM

compound installed
- binary: $install_dir/compound
- breadth_engine: $install_dir/compound-breadth-engine
- home: $compound_home

next:
  export PATH="$install_dir:\$PATH"
  compound version
  compound workbench
EOM
