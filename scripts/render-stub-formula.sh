#!/usr/bin/env bash
#
# Render a Homebrew formula stub from the first `brews:` entry of
# .goreleaser.yaml. The stub uses placeholder URL/sha256/version so it
# clears `brew style` + `brew audit --strict` offline, in seconds,
# without building any binaries.
#
# Inputs (from .goreleaser.yaml):
#   .brews[0].name         -> class name (kebab-case -> PascalCase) and filename
#   .brews[0].description  -> desc
#   .brews[0].homepage     -> homepage
#   .brews[0].license      -> license
#   .brews[0].install      -> def install body (copied byte-for-byte)
#   .brews[0].test         -> test do body (copied byte-for-byte)
#
# Outputs to stdout. Requires `yq` (Mike Farah's Go yq, preinstalled on
# GitHub-hosted ubuntu-latest).

set -euo pipefail

YAML="${1:-.goreleaser.yaml}"

if ! command -v yq >/dev/null 2>&1; then
  echo "render-stub-formula.sh: yq is required" >&2
  exit 1
fi

if [ ! -f "$YAML" ]; then
  echo "render-stub-formula.sh: $YAML not found" >&2
  exit 1
fi

name=$(yq '.brews[0].name' "$YAML")
desc=$(yq '.brews[0].description' "$YAML")
homepage=$(yq '.brews[0].homepage' "$YAML")
license=$(yq '.brews[0].license' "$YAML")
install_block=$(yq '.brews[0].install' "$YAML")
test_block=$(yq '.brews[0].test' "$YAML")

for field in name desc homepage license install_block test_block; do
  if [ -z "${!field}" ] || [ "${!field}" = "null" ]; then
    echo "render-stub-formula.sh: .brews[0].$field is missing in $YAML" >&2
    exit 1
  fi
done

# kebab-case -> PascalCase (skills-oci -> SkillsOci)
class_name=$(printf '%s\n' "$name" | awk -F'-' '{
  out=""
  for (i = 1; i <= NF; i++) out = out toupper(substr($i,1,1)) substr($i,2)
  print out
}')

# Indent each line by 4 spaces, matching Homebrew formula body depth.
indent4() { sed 's/^/    /'; }

cat <<EOF
class ${class_name} < Formula
  desc "${desc}"
  homepage "${homepage}"
  url "https://example.com/${name}-0.0.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "${license}"

  def install
$(printf '%s\n' "$install_block" | indent4)
  end

  test do
$(printf '%s\n' "$test_block" | indent4)
  end
end
EOF
