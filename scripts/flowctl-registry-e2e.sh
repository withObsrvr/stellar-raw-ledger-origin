#!/usr/bin/env bash
set -euo pipefail

# Registry/image-path smoke for:
#   raw-ledger-source@0.2.5 -> contract-events-processor -> smoke sink
#
# The source is resolved through flowctl's registry component resolver. To keep
# the smoke local and deterministic, this script pre-populates flowctl's OCI
# image cache with a docker-built image at the exact cache path the resolver
# expects for docker.io/withobsrvr/raw-ledger-source:0.2.5. flowctl then
# extracts /app/component and /metadata.json into ~/.flowctl/components and
# uses metadata env mappings to translate source config.

ROOT_DIR=${ROOT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
FLOWCTL_BIN=${FLOWCTL_BIN:-/home/tillman/Documents/flowctl/bin/flowctl}
FLOWCTL_SDK_DIR=${FLOWCTL_SDK_DIR:-/home/tillman/Documents/flowctl-sdk}
FLOW_PROTO_DIR=${FLOW_PROTO_DIR:-/home/tillman/Documents/flow-proto}
CONTRACT_EVENTS_PROCESSOR_BIN=${CONTRACT_EVENTS_PROCESSOR_BIN:-$FLOWCTL_SDK_DIR/examples/contract-events-processor/bin/contract-events-processor}
RPC_ENDPOINT=${RPC_ENDPOINT:-https://soroban-testnet.stellar.org}
NETWORK_PASSPHRASE=${NETWORK_PASSPHRASE:-Test SDF Network ; September 2015}
CONTROL_PLANE_PORT=${CONTROL_PLANE_PORT:-18090}
RUN_TIMEOUT=${RUN_TIMEOUT:-45s}
LEDGER_COUNT_BACK=${LEDGER_COUNT_BACK:-120}
LEDGER_COUNT_END_OFFSET=${LEDGER_COUNT_END_OFFSET:-20}
COMPONENT_NAME=${COMPONENT_NAME:-raw-ledger-source}
COMPONENT_VERSION=${COMPONENT_VERSION:-0.2.5}
COMPONENT_REF=${COMPONENT_REF:-${COMPONENT_NAME}@${COMPONENT_VERSION}}
IMAGE_REF=${IMAGE_REF:-docker.io/withobsrvr/${COMPONENT_NAME}:${COMPONENT_VERSION}}

require_file() {
  if [[ ! -x "$1" ]]; then
    echo "missing executable: $1" >&2
    exit 1
  fi
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 1
  fi
}

require_file "$FLOWCTL_BIN"
require_file "$CONTRACT_EVENTS_PROCESSOR_BIN"
require_cmd docker
require_cmd curl
require_cmd jq
require_cmd timeout

TMPDIR=$(mktemp -d)
cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

SMOKE_SINK_BIN="$TMPDIR/flowctl-smoke-sink"
PIPELINE="$TMPDIR/pipeline.yaml"
OUT="$TMPDIR/flowctl.out"
LOG_DIR="$TMPDIR/logs"
DB_PATH="$TMPDIR/flowctl.db"
mkdir -p "$LOG_DIR"

FLOWCTL_HOME=${FLOWCTL_HOME:-$HOME/.flowctl}
IMAGE_CACHE="$FLOWCTL_HOME/cache/images/docker.io/withobsrvr/${COMPONENT_NAME}/${COMPONENT_VERSION}.tar"
EXTRACTED_CACHE="$IMAGE_CACHE.extracted"
COMPONENT_CACHE="$FLOWCTL_HOME/components/${COMPONENT_NAME}/${COMPONENT_VERSION}"

printf 'Building flowctl source image %s...\n' "$IMAGE_REF"
docker build -f "$ROOT_DIR/Dockerfile.flowctl" -t "$IMAGE_REF" "$ROOT_DIR" >/dev/null

printf 'Priming flowctl OCI cache: %s\n' "$IMAGE_CACHE"
rm -rf "$EXTRACTED_CACHE" "$COMPONENT_CACHE"
mkdir -p "$(dirname "$IMAGE_CACHE")"
docker save "$IMAGE_REF" -o "$IMAGE_CACHE"

printf 'Building temp smoke sink...\n'
mkdir -p "$TMPDIR/smoke-sink"
cat > "$TMPDIR/smoke-sink/go.mod" <<EOF2
module raw-ledger-source-registry-e2e-sink

go 1.26.1

require github.com/withObsrvr/flowctl-sdk v0.1.4

replace github.com/withObsrvr/flowctl-sdk => $FLOWCTL_SDK_DIR
replace github.com/withObsrvr/flow-proto => $FLOW_PROTO_DIR
EOF2
cat > "$TMPDIR/smoke-sink/main.go" <<'EOF2'
package main

import (
	"context"
	"fmt"
	"sync/atomic"

	flowctlv1 "github.com/withObsrvr/flow-proto/go/gen/flowctl/v1"
	"github.com/withObsrvr/flowctl-sdk/pkg/consumer"
)

var count uint64

func main() {
	consumer.Run(consumer.ConsumerConfig{
		ConsumerName: "Registry Smoke Sink",
		InputTypes:   []string{"stellar.contract.event.v1"},
		OnEvent: func(ctx context.Context, event *flowctlv1.Event) error {
			n := atomic.AddUint64(&count, 1)
			fmt.Printf("SMOKE_SINK_EVENT count=%d id=%s type=%s payload=%d\n", n, event.GetId(), event.GetType(), len(event.GetPayload()))
			return nil
		},
	})
	fmt.Printf("SMOKE_SINK_TOTAL count=%d\n", atomic.LoadUint64(&count))
}
EOF2
(cd "$TMPDIR/smoke-sink" && go mod tidy >/dev/null && go build -buildvcs=false -o "$SMOKE_SINK_BIN" .)

LATEST=$(curl -fsS "$RPC_ENDPOINT" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"getLatestLedger"}' \
  | jq -r '.result.sequence')
START=$((LATEST-LEDGER_COUNT_BACK))
END=$((LATEST-LEDGER_COUNT_END_OFFSET))

cat > "$PIPELINE" <<EOF2
apiVersion: flowctl/v1
kind: Pipeline
metadata:
  name: raw-ledger-source-registry-e2e
  description: Registry/image-path smoke for raw-ledger-source -> contract-events -> sink
spec:
  driver: process
  sources:
    - id: stellar-source
      type: $COMPONENT_REF
      config:
        backend_type: RPC
        rpc_endpoint: $RPC_ENDPOINT
        network_passphrase: "$NETWORK_PASSPHRASE"
        start_ledger: $START
        end_ledger: $END
        grpc_port: 50081
        health_port: 18091
  processors:
    - id: contract-events
      type: processor
      command: ["$CONTRACT_EVENTS_PROCESSOR_BIN"]
      inputs: ["stellar-source"]
      env:
        COMPONENT_ID: "contract-events"
        NETWORK_PASSPHRASE: "$NETWORK_PASSPHRASE"
        PORT: ":50082"
        HEALTH_PORT: "18092"
        ENABLE_FLOWCTL: "true"
        FLOWCTL_ENDPOINT: "127.0.0.1:$CONTROL_PLANE_PORT"
  sinks:
    - id: smoke-sink
      type: sink
      command: ["$SMOKE_SINK_BIN"]
      inputs: ["contract-events"]
      env:
        COMPONENT_ID: "smoke-sink"
        PORT: ":50083"
        HEALTH_PORT: "18093"
        ENABLE_FLOWCTL: "true"
        FLOWCTL_ENDPOINT: "127.0.0.1:$CONTROL_PLANE_PORT"
EOF2

printf 'Running flowctl registry smoke ref=%s latest=%s start=%s end=%s timeout=%s...\n' "$COMPONENT_REF" "$LATEST" "$START" "$END" "$RUN_TIMEOUT"
set +e
timeout "$RUN_TIMEOUT" "$FLOWCTL_BIN" run \
  --control-plane-port "$CONTROL_PLANE_PORT" \
  --db-path "$DB_PATH" \
  --log-dir "$LOG_DIR" \
  "$PIPELINE" 2>&1 | tee "$OUT"
RC=${PIPESTATUS[0]}
set -e

if [[ "$RC" != "0" && "$RC" != "124" ]]; then
  echo "flowctl failed with rc=$RC" >&2
  exit "$RC"
fi

if [[ ! -x "$COMPONENT_CACHE/component" ]]; then
  echo "registry smoke failed: missing cached component binary at $COMPONENT_CACHE/component" >&2
  exit 1
fi
if [[ ! -f "$COMPONENT_CACHE/metadata.json" ]]; then
  echo "registry smoke failed: missing cached metadata at $COMPONENT_CACHE/metadata.json" >&2
  exit 1
fi

CACHE_TYPE=$(jq -r '.type' "$COMPONENT_CACHE/metadata.json")
CACHE_PRODUCES=$(jq -r '.produces | join(",")' "$COMPONENT_CACHE/metadata.json")
CACHE_RPC_ENV=$(jq -r '.env_mapping.rpc_endpoint' "$COMPONENT_CACHE/metadata.json")
if [[ "$CACHE_TYPE" != "source" || "$CACHE_PRODUCES" != "stellar.ledger.v1" || "$CACHE_RPC_ENV" != "RPC_ENDPOINT" ]]; then
  echo "registry smoke failed: unexpected cached metadata type=$CACHE_TYPE produces=$CACHE_PRODUCES rpc_env=$CACHE_RPC_ENV" >&2
  exit 1
fi

TOTAL=$(grep -Eo 'SMOKE_SINK_TOTAL count=[0-9]+' "$OUT" | tail -1 | cut -d= -f2 || true)
if [[ -z "$TOTAL" ]]; then
  TOTAL=$(grep -Ec 'SMOKE_SINK_EVENT' "$OUT" || true)
fi
if [[ "${TOTAL:-0}" -lt 1 ]]; then
  echo "registry smoke failed: sink received no events" >&2
  exit 1
fi

printf 'Registry smoke passed: sink received %s event(s); cached %s with metadata type=%s produces=%s.\n' "$TOTAL" "$COMPONENT_REF" "$CACHE_TYPE" "$CACHE_PRODUCES"
