#!/usr/bin/env bash
# Build the release archives for one version and write the Homebrew formula
# that installs them.
#
#   scripts/release.sh v0.1.0
#
# Output: dist/spellbook_<version>_<os>_<arch>.tar.gz, dist/checksums.txt
# and Formula/spellbook.rb. Publishing the archives is the job of the
# release workflow. Needs bash 4 or newer.
set -euo pipefail

tag="${1:?usage: scripts/release.sh <tag, for example v0.1.0>}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: the tag must look like v1.2.3, got: $tag" >&2
  exit 1
fi
version="${tag#v}"
repo="${SPELLBOOK_REPO:-graemer-org/spellbook}"
module="github.com/graemer-org/spellbook"

cd "$(dirname "$0")/.."
rm -rf dist
mkdir -p dist Formula

sha() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

declare -A sums
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  os="${target%/*}"
  arch="${target#*/}"
  work="dist/build_${os}_${arch}"
  mkdir -p "$work"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X ${module}/internal/cli.Version=${version}" \
    -o "$work/spellbook" ./cmd/spellbook
  cp README.md "$work/"
  archive="spellbook_${version}_${os}_${arch}.tar.gz"
  tar -C "$work" -czf "dist/$archive" spellbook README.md
  rm -rf "$work"
  sums["${os}_${arch}"]="$(sha "dist/$archive")"
  echo "${sums["${os}_${arch}"]}  $archive" >> dist/checksums.txt
done

url() { echo "https://github.com/${repo}/releases/download/${tag}/spellbook_${version}_$1.tar.gz"; }

cat > Formula/spellbook.rb <<FORMULA
# Written by scripts/release.sh. Do not edit.
class Spellbook < Formula
  desc "Share AI skills, instructions, agents and MCP servers from git registries"
  homepage "https://github.com/${repo}"
  version "${version}"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "$(url darwin_arm64)"
      sha256 "${sums[darwin_arm64]}"
    end
    on_intel do
      url "$(url darwin_amd64)"
      sha256 "${sums[darwin_amd64]}"
    end
  end

  on_linux do
    on_arm do
      url "$(url linux_arm64)"
      sha256 "${sums[linux_arm64]}"
    end
    on_intel do
      url "$(url linux_amd64)"
      sha256 "${sums[linux_amd64]}"
    end
  end

  def install
    bin.install "spellbook"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/spellbook --version")
  end
end
FORMULA

echo "Built $(ls dist/*.tar.gz | wc -l | tr -d ' ') archives for ${tag} and wrote Formula/spellbook.rb"
