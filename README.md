# stellar-raw-ledger-origin

One raw-ledger core, two runtime adapters.

A small, adapter-neutral core converts a Stellar `xdr.LedgerCloseMeta`
into a `RawLedger` event. Two thin adapters wrap that core:

| Binary | Style | Status |
| --- | --- | --- |
| `raw-ledger-origin` | nebu CLI processor — emits JSONL on stdout, Unix-pipe composable | **Done (M1)** |
| `raw-ledger-source` | flowctl source service — registration, health, heartbeats, gRPC | **Done (M2, RPC backend)** |

```
                  rawledger  (pure: LedgerCloseMeta -> RawLedger)
                     │
        ┌────────────┴────────────┐
   raw-ledger-origin        raw-ledger-source
   (nebu CLI adapter)       (flowctl service)
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
  for the shared core (via a relative `replace` during development).

## Schema

`RawLedger` is **wire- and name-identical** to `stellar.v1.RawLedger`
in [`flow-proto`](https://github.com/withObsrvr/flow-proto): same proto
package (`stellar.v1`), same message name, same field numbers. This repo
keeps a lean, **messages-only** copy (no gRPC service) so the nebu binary
stays small and flowctl-free. A guard test
(`rawledger/raw_ledger_sync_test.go`) fails if the two ever drift.

The flowctl source advertises this same type name (`stellar.v1.RawLedger`)
and carries the marshalled `RawLedger` as the `Event` payload, so a
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
| `BACKEND_TYPE` | `RPC` | `RPC` (wired) · `ARCHIVE` / `CAPTIVE_CORE` (seams) |
| `RPC_ENDPOINT` | — | Stellar RPC URL (RPC backend) |
| `RPC_AUTH_HEADER` | — | optional `Authorization` header |
| `NETWORK_PASSPHRASE` | pubnet | network passphrase stamped on events |
| `START_LEDGER` / `END_LEDGER` | `0` | default range if a stream omits them (`END_LEDGER=0` = unbounded) |
| `GRPC_PORT` / `HEALTH_PORT` | `50051` / `8088` | listen ports |
| `FLOWCTL_ENDPOINT` | — | control plane; registration enabled when set |

Container image: `docker build -f Dockerfile.flowctl -t raw-ledger-source .`
(build context must be the repo root so the `replace => ../` resolves).

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
```

Root built against tagged `github.com/withObsrvr/nebu v0.6.7`; source
against `flowctl-sdk v0.1.1` / `flow-proto v0.1.3`.

## Layout

The origin binary lives under `processors/<name>/cmd/<name>/` so it
matches the module path nebu's external registry derives for
`nebu install` (`github.com/<repo>/processors/<name>/cmd/<name>`).

```
proto/stellar/v1/raw_ledger.proto                      canonical (messages-only) schema
gen/stellar/v1/raw_ledger.pb.go                        generated; committed for convenience
rawledger/                                             shared core: ToRawLedger + guard tests
processors/raw-ledger-origin/cmd/raw-ledger-origin/    nebu CLI adapter (owns nebu coupling)
description.yml                                         nebu external-registry descriptor
flake.nix / Makefile                                   reproducible build + codegen (root)
source/                                                flowctl module (go 1.26)
  go.mod                                                  replace => ../ for the shared core
  cmd/raw-ledger-source/                                 flowctl adapter (producer + wiring)
  internal/backend/                                      backend selection (RPC wired)
Dockerfile.flowctl                                     source container (context = repo root)
```
