# stellar-raw-ledger-origin

One raw-ledger core, two runtime adapters.

A small, adapter-neutral core converts a parsed Stellar
`xdr.LedgerCloseMeta` or canonical LedgerCloseMeta XDR into a `RawLedger`
event. Two thin adapters wrap that core:

| Binary | Style | Status |
| --- | --- | --- |
| `raw-ledger-origin` | nebu CLI processor — emits JSONL on stdout, Unix-pipe composable | **Done (M1)** |
| `raw-ledger-source` | flowctl source service — registration, health, heartbeats, gRPC | **Done (RPC, archive, and captive-core streams)** |
| `ledger-stream-bench` | acquisition-only throughput and correctness benchmark | **Done** |

```
                rawledger  (LedgerCloseMeta/XDR -> RawLedger)
                    │
       ┌────────────┴────────────┐
  raw-ledger-origin        raw-ledger-source
  (nebu CLI adapter)       (flowctl service)
                                  │
                             ledgerstream
                         (SDK LedgerStream factory)
```

The core owns **event correctness**; the adapters own **runtime
behaviour**. The core depends only on the Stellar XDR types and the
generated `RawLedger` message — never on Cobra, stdout, flowctl, gRPC,
or backend configuration.

### Two modules

The repo is two Go modules so the lean origin stays free of gRPC/flowctl:

- **root** (`go 1.25`): shared core (`rawledger`), proto, and the nebu
  `raw-ledger-origin` binary. No gRPC.
- **`source/`** (`go 1.26`): the flowctl `raw-ledger-source` service.
  Depends on `flowctl-sdk` + `flow-proto` + gRPC, and on the root module
  for the shared core (pinned to the published root `v0.2.3` tag).

## Schema

`RawLedger` is **wire- and name-identical** to `stellar.v1.RawLedger`
in [`flow-proto`](https://github.com/withObsrvr/flow-proto): same proto
package (`stellar.v1`), same message name, same field numbers. This repo
keeps a lean, **messages-only** copy (no gRPC service) so the nebu binary
stays small and flowctl-free. A guard test
(`rawledger/raw_ledger_sync_test.go`) fails if the two ever drift.

The flowctl source advertises the existing routing type `stellar.ledger.v1`
and carries the marshalled `stellar.v1.RawLedger` as the `Event` payload, so a
consumer decoding with flow-proto's type interoperates with both adapters.

```proto
package stellar.v1;
message RawLedger {
  uint32 sequence = 1;
  bytes  ledger_close_meta_xdr = 2;
  string network = 3;
  int64  closed_at = 4;
}
```

nebu JSONL output (schema id `nebu.raw_ledger.v1`):

```json
{
  "_schema": "nebu.raw_ledger.v1",
  "_nebu_version": "0.x.x",
  "sequence": 60200000,
  "ledgerCloseMetaXdr": "base64...",
  "network": "Public Global Stellar Network ; September 2015",
  "closedAt": "1700000000"
}
```

## Usage

```bash
# Bounded range over RPC
raw-ledger-origin --start-ledger 60200000 --end-ledger 60200010 | jq .

# Compose with nebu fetch
nebu fetch 60200000 60200010 | raw-ledger-origin | jq .

# Read XDR from a file or stdin
raw-ledger-origin ledgers.xdr | jq .
cat ledgers.xdr | raw-ledger-origin - | jq .

# Machine-readable description
raw-ledger-origin --describe-json | jq .
```

Diagnostics go to stderr; only JSONL events go to stdout.

### flowctl source (`raw-ledger-source`)

A long-running service. Configuration is via environment variables;
service lifecycle (registration, heartbeats, health, gRPC) is handled by
`flowctl-sdk`.

```bash
cd source
RPC_ENDPOINT=https://mainnet.rpc.example \
START_LEDGER=60200000 END_LEDGER=60200010 \
FLOWCTL_ENDPOINT=localhost:8080 \
go run ./cmd/raw-ledger-source
```

| Env | Default | Meaning |
| --- | --- | --- |
| `BACKEND_TYPE` | `RPC` | `RPC` · `ARCHIVE` · `CAPTIVE_CORE` |
| `RPC_ENDPOINT` | — | Stellar RPC URL (RPC backend) |
| `RPC_AUTH_HEADER` | — | optional `Authorization` header |
| `ARCHIVE_STORAGE_TYPE` | — | `GCS` or `S3` (ARCHIVE backend; `STORAGE_TYPE` fallback) |
| `ARCHIVE_BUCKET_NAME` | — | archive bucket (ARCHIVE backend; `BUCKET_NAME` fallback) |
| `ARCHIVE_PATH` | — | optional prefix inside the archive bucket (`BUCKET_PATH` fallback) |
| `LEDGERS_PER_FILE` / `FILES_PER_PARTITION` | `1` / `64000` | archive datastore schema; values are checked against the remote manifest when present |
| `BUFFER_SIZE` / `NUM_WORKERS` | `5` / `2` | archive read-ahead and worker count |
| `AWS_REGION` / `S3_ENDPOINT_URL` | `us-east-1` / — | S3 archive region and optional S3-compatible endpoint |
| `HISTORY_ARCHIVE_URLS` | — | comma-separated Stellar history archives (CAPTIVE_CORE backend) |
| `STELLAR_CORE_BINARY_PATH` | — | path to `stellar-core` (CAPTIVE_CORE backend) |
| `STELLAR_CORE_CONFIG_PATH` | — | optional base stellar-core config file (CAPTIVE_CORE backend) |
| `CAPTIVE_CORE_STORAGE_PATH` | — | optional captive-core bucket storage base path |
| `NETWORK_PASSPHRASE` | pubnet | network passphrase stamped on events |
| `START_LEDGER` / `END_LEDGER` | `0` | default range if a stream omits them (`END_LEDGER=0` = unbounded) |
| `GRPC_PORT` / `HEALTH_PORT` | `50051` / `8088` | gRPC and SDK health/JSON metrics ports |
| `METRICS_PORT` | `9095` | separate Prometheus `/metrics` and source `/status` JSON (last fetched/queued ledger; not a durable cursor) |
| `FLOWCTL_ENDPOINT` | — | control plane; registration enabled when set |
| `FLOWCTL_COMPONENT_ID` / `SOURCE_ID` | `raw-ledger-source` | component id; flowctl-injected `FLOWCTL_COMPONENT_ID` wins |

Container image: `docker build -f Dockerfile.flowctl -t raw-ledger-source .`
(build context is the repo root for the metadata and source module). The
image follows the flowctl resolver layout: executable at `/app/component` and
component metadata at `/metadata.json`.

### Acquisition benchmark

`ledger-stream-bench` measures only raw archive/RPC/captive-core acquisition.
It verifies the exact ledger sequence and reports ledger and byte throughput as
JSON, without extraction, protobuf expansion, or persistence in the measured
interval.

```bash
cd source
BACKEND_TYPE=ARCHIVE \
ARCHIVE_STORAGE_TYPE=S3 \
ARCHIVE_BUCKET_NAME=aws-public-blockchain \
ARCHIVE_PATH=v1.1/stellar/ledgers/pubnet \
AWS_REGION=us-east-2 \
BUFFER_SIZE=1000 \
NUM_WORKERS=50 \
go run ./cmd/ledger-stream-bench \
  --start-ledger 62080000 \
  --end-ledger 62080999
```

For the production acquisition gate, sweep `NUM_WORKERS` and `BUFFER_SIZE`
rather than assuming the example values are optimal. The command defaults to
one ledger per object and 64,000 objects per path partition.

The first 1,000-ledger mainnet result and its interpretation are recorded in
[`docs/ledger-stream-benchmark-2026-08-05.md`](docs/ledger-stream-benchmark-2026-08-05.md).

## Build

Everything runs inside the nix dev shell (provides Go, `protoc`,
`protoc-gen-go`, `gcc` for the SDK's cgo, `make`):

```bash
nix develop

make proto    # regenerate gen/ from proto/ (messages-only)
make build    # go build ./...  (root module: core + origin)
make test     # go test ./...
make describe # smoke-test --describe-json

# source module (go 1.26)
cd source && go build ./... && go test ./...

# Nix packages
nix build .#raw-ledger-origin
nix build .#raw-ledger-source
make packaging-check

# flowctl process-driver end-to-end smoke
# Requires local flowctl, flowctl-sdk, flow-proto checkouts at the default paths
# or set FLOWCTL_BIN / FLOWCTL_SDK_DIR / FLOW_PROTO_DIR.
RUN_TIMEOUT=45s scripts/flowctl-process-e2e.sh

# flowctl registry/image resolver smoke for raw-ledger-source@0.2.5
RUN_TIMEOUT=45s scripts/flowctl-registry-e2e.sh
```

The flowctl smokes build `raw-ledger-source`, generate a temporary sink,
and run `raw-ledger-source -> contract-events-processor -> smoke-sink`
over testnet RPC. `flowctl run` is long-lived, so a timeout exit is treated
as success if the sink received at least one event. The registry smoke primes
flowctl's local OCI cache for `docker.io/withobsrvr/raw-ledger-source:0.2.5`
and verifies type-reference resolution, image extraction, component caching,
and metadata-driven config-to-env translation.

Root is built against tagged `github.com/withObsrvr/nebu v0.6.15`; source is
built against `flowctl-sdk v0.1.4` / `flow-proto v0.1.3`. Both modules pin
`go-stellar-sdk v0.7.3`; the stream and XDR-view APIs are kept behind this
repository's small public boundary while those SDK APIs remain experimental.

## Layout

The origin binary lives under `processors/<name>/cmd/<name>/` so it
matches the module path nebu's external registry derives for
`nebu install` (`github.com/<repo>/processors/<name>/cmd/<name>`).

```
proto/stellar/v1/raw_ledger.proto                      canonical (messages-only) schema
gen/stellar/v1/raw_ledger.pb.go                        generated; committed for convenience
rawledger/                                             shared core: parsed and raw-XDR conversion + guard tests
processors/raw-ledger-origin/cmd/raw-ledger-origin/    nebu CLI adapter (owns nebu coupling)
description.yml                                         nebu external-registry descriptor
flake.nix / Makefile                                   reproducible build + codegen (root)
source/                                                flowctl module (go 1.26)
  go.mod                                                  pinned root tag for the shared core
  cmd/raw-ledger-source/                                 flowctl adapter (producer + wiring)
  cmd/ledger-stream-bench/                               acquisition-only performance gate
  ledgerstream/                                          public SDK LedgerStream configuration/factory
scripts/flowctl-process-e2e.sh                         process-driver flowctl smoke test
scripts/flowctl-registry-e2e.sh                        registry/image resolver flowctl smoke test
scripts/check-packaging.sh                             version/metadata packaging guard
Dockerfile.flowctl                                     source container (context = repo root)
metadata.json                                          flowctl image metadata + config env mappings
```
