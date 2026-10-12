#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=${ROOT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
cd "$ROOT_DIR"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 1
  fi
}

require_cmd jq

metadata_version=$(jq -r '.version' metadata.json)
description_version=$(awk '/^  version: / {print $2; exit}' description.yml)
flake_version=$(grep -E '^[[:space:]]*version = "[0-9]+\.[0-9]+\.[0-9]+";' flake.nix | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
docker_version=$(grep -E 'LABEL org.opencontainers.image.version=' Dockerfile.flowctl | sed -E 's/.*="([^"]+)".*/\1/')
root_require_version=$(awk '$1 == "github.com/withObsrvr/stellar-raw-ledger-origin" {print $2; exit}' source/go.mod | sed 's/^v//')
binary_version=$(grep -E '^const version = "[0-9]+\.[0-9]+\.[0-9]+"' source/cmd/raw-ledger-source/main.go | sed -E 's/.*"([^\"]+)"/\1/')
description_ref=$(awk '/^  ref: / {print $2; exit}' description.yml)

component_versions=(
  "metadata.json:$metadata_version"
  "description.yml:$description_version"
  "flake.nix:$flake_version"
  "Dockerfile.flowctl:$docker_version"
  "source/cmd/raw-ledger-source/main.go:$binary_version"
)

want="$metadata_version"
for entry in "${component_versions[@]}"; do
  name=${entry%%:*}
  got=${entry#*:}
  if [[ -z "$got" || "$got" != "$want" ]]; then
    echo "version mismatch: $name has $got, want $want" >&2
    printf 'all component versions:\n' >&2
    printf '  %s\n' "${component_versions[@]}" >&2
    exit 1
  fi
done

if [[ "$description_ref" != "v$want" ]]; then
  echo "description.yml ref $description_ref does not match v$want" >&2
  exit 1
fi
if grep -Eq '^replace github.com/withObsrvr/stellar-raw-ledger-origin => \.\./' source/go.mod; then
  echo 'source/go.mod must not contain a relative root replace for release' >&2
  exit 1
fi

if [[ -z "$root_require_version" ]]; then
  echo "missing root module requirement in source/go.mod" >&2
  exit 1
fi

# v0.2.0 is the first root tag that exposes rawledger/ outside internal/.
min_root_version="0.2.0"
oldest=$(printf '%s\n%s\n' "$min_root_version" "$root_require_version" | sort -V | head -1)
if [[ "$oldest" != "$min_root_version" ]]; then
  echo "source/go.mod requires root module v$root_require_version; need >= v$min_root_version for rawledger/" >&2
  exit 1
fi

jq -e '.type == "source" and (.produces | index("stellar.ledger.v1")) and .env_mapping.backend_type == "BACKEND_TYPE"' metadata.json >/dev/null

echo "Packaging check passed for component version $want (root module requirement v$root_require_version)"
printf '  %s\n' "${component_versions[@]}"
printf '  source/go.mod root requirement:%s\n' "$root_require_version"
