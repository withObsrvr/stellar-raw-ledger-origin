#!/usr/bin/env bash
set -euo pipefail

# Local process-driver smoke for:
#   raw-ledger-source -> contract-events-processor -> smoke sink
#
# The smoke sink is generated into a temp module so the repo does not carry a
# test-only component. A timeout exit (124) is acceptable if the sink received
# at least one event; flowctl runs until interrupted.

ROOT_DIR=${ROOT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
FLOWCTL_BIN=${FLOWCTL_BIN:-/home/tillman/Documents/flowctl/bin/flowctl}
FLOWCTL_SDK_DIR=${FLOWCTL_SDK_DIR:-/home/tillman/Documents/flowctl-sdk}
FLOW_PROTO_DIR=${FLOW_PROTO_DIR:-/home/tillman/Documents/flow-proto}
CONTRACT_EVENTS_PROCESSOR_BIN=${CONTRACT_EVENTS_PROCESSOR_BIN:-$FLOWCTL_SDK_DIR/examples/contract-events-processor/bin/contract-events-processor}
RPC_ENDPOINT=${RPC_ENDPOINT:-https://soroban-testnet.stellar.org}
NETWORK_PASSPHRASE=${NETWORK_PASSPHRASE:-Test SDF Network ; September 2015}
CONTROL_PLANE_PORT=${CONTROL_PLANE_PORT:-18080}
RUN_TIMEOUT=${RUN_TIMEOUT:-45s}
LEDGER_COUNT_BACK=${LEDGER_COUNT_BACK:-120}
LEDGER_COUNT_END_OFFSET=${LEDGER_COUNT_END_OFFSET:-20}

require_file() {
  if [[ ! -x "$1" ]]; then
    echo "missing executable: $1" >&2
    exit 1
  fi
}

require_file "$FLOWCTL_BIN"
require_file "$CONTRACT_EVENTS_PROCESSOR_BIN"

TMPDIR=$(mktemp -d)
cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

SOURCE_BIN="$TMPDIR/raw-ledger-source"
SMOKE_SINK_BIN="$TMPDIR/flowctl-smoke-sink"
PIPELINE="$TMPDIR/pipeline.yaml"
OUT="$TMPDIR/flowctl.out"
LOG_DIR="$TMPDIR/logs"
DB_PATH="$TMPDIR/flowctl.db"
mkdir -p "$LOG_DIR"

printf 'Building raw-ledger-source...\n'
(cd "$ROOT_DIR/source" && go build -o "$SOURCE_BIN" ./cmd/raw-ledger-source)

printf 'Building temp smoke sink...\n'
mkdir -p "$TMPDIR/smoke-sink"
cat > "$TMPDIR/smoke-sink/go.mod" <<EOF2
module raw-ledger-source-e2e-sink

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
		ConsumerName: "Smoke Sink",
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
  name: raw-ledger-source-process-e2e
  description: Local process smoke for raw-ledger-source -> contract-events -> sink
spec:
  driver: process
  sources:
    - id: stellar-source
      type: source
      command: ["$SOURCE_BIN"]
      env:
        FLOWCTL_COMPONENT_ID: "stellar-source"
        BACKEND_TYPE: "RPC"
        RPC_ENDPOINT: "$RPC_ENDPOINT"
        NETWORK_PASSPHRASE: "$NETWORK_PASSPHRASE"
        START_LEDGER: "$START"
        END_LEDGER: "$END"
        GRPC_PORT: "50071"
        HEALTH_PORT: "18081"
        FLOWCTL_ENDPOINT: "127.0.0.1:$CONTROL_PLANE_PORT"
  processors:
    - id: contract-events
      type: processor
      command: ["$CONTRACT_EVENTS_PROCESSOR_BIN"]
      inputs: ["stellar-source"]
      env:
        COMPONENT_ID: "contract-events"
        NETWORK_PASSPHRASE: "$NETWORK_PASSPHRASE"
        PORT: ":50072"
        HEALTH_PORT: "18082"
        ENABLE_FLOWCTL: "true"
        FLOWCTL_ENDPOINT: "127.0.0.1:$CONTROL_PLANE_PORT"
  sinks:
    - id: smoke-sink
      type: sink
      command: ["$SMOKE_SINK_BIN"]
      inputs: ["contract-events"]
      env:
        COMPONENT_ID: "smoke-sink"
        PORT: ":50073"
        HEALTH_PORT: "18083"
        ENABLE_FLOWCTL: "true"
        FLOWCTL_ENDPOINT: "127.0.0.1:$CONTROL_PLANE_PORT"
EOF2

printf 'Running flowctl process smoke latest=%s start=%s end=%s timeout=%s...\n' "$LATEST" "$START" "$END" "$RUN_TIMEOUT"
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

TOTAL=$(grep -Eo 'SMOKE_SINK_TOTAL count=[0-9]+' "$OUT" | tail -1 | cut -d= -f2 || true)
if [[ -z "$TOTAL" ]]; then
  TOTAL=$(grep -Ec 'SMOKE_SINK_EVENT' "$OUT" || true)
fi
if [[ "${TOTAL:-0}" -lt 1 ]]; then
  echo "smoke failed: sink received no events" >&2
  exit 1
fi

printf 'Smoke passed: sink received %s event(s).\n' "$TOTAL"
