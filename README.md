# stellar-raw-ledger-origin

One raw-ledger core, two runtime adapters.

A small, adapter-neutral core converts a Stellar `xdr.LedgerCloseMeta`
into a `RawLedger` event. Two thin adapters wrap that core:

| Binary | Style | Status |
| --- | --- | --- |
| `raw-ledger-origin` | nebu CLI processor — emits JSONL on stdout, Unix-pipe composable | **Milestone 1 (this repo)** |
| `raw-ledger-source` | flowctl source service — registration, health, heartbeats, gRPC | Milestone 2 (planned) |

```
            internal/rawledger  (pure: LedgerCloseMeta -> RawLedger)
                     │
        ┌────────────┴────────────┐
   raw-ledger-origin        raw-ledger-source
   (nebu CLI adapter)       (flowctl service, M2)
```

The core owns **event correctness**; the adapters own **runtime
behaviour**. The core depends only on the Stellar XDR types and the
generated `RawLedger` message — never on Cobra, stdout, flowctl, gRPC,
or backend configuration.

## Schema

`RawLedger` is **wire- and name-identical** to `stellar.v1.RawLedger`
in [`flow-proto`](https://github.com/withObsrvr/flow-proto): same proto
package (`stellar.v1`), same message name, same field numbers. This repo
keeps a lean, **messages-only** copy (no gRPC service) so the nebu binary
stays small and flowctl-free. A guard test
(`internal/rawledger/raw_ledger_sync_test.go`) fails if the two ever
drift.

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

## Build

Everything runs inside the nix dev shell (provides Go, `protoc`,
`protoc-gen-go`, `gcc` for the SDK's cgo, `make`):

```bash
nix develop

make proto    # regenerate gen/ from proto/ (messages-only)
make build    # go build ./...
make test     # go test ./...
make describe # smoke-test --describe-json
```

Built against tagged `github.com/withObsrvr/nebu v0.6.7`.

## Layout

The origin binary lives under `processors/<name>/cmd/<name>/` so it
matches the module path nebu's external registry derives for
`nebu install` (`github.com/<repo>/processors/<name>/cmd/<name>`).

```
proto/stellar/v1/raw_ledger.proto                      canonical (messages-only) schema
gen/stellar/v1/raw_ledger.pb.go                        generated; committed for convenience
internal/rawledger/                                    shared core: ToRawLedger + guard tests
processors/raw-ledger-origin/cmd/raw-ledger-origin/    nebu CLI adapter (owns nebu coupling)
description.yml                                         nebu external-registry descriptor
flake.nix / Makefile                                   reproducible build + codegen
```
