# Handoff — Milestone 2: flowctl `raw-ledger-source`

**Status:** Tracer bullet complete and verified locally. The service
builds, vets, unit-tests pass, and it runs: the health endpoint serves
JSON and the gRPC server binds. The shared core is now reused by both
adapters with zero duplication.

## What was built

| Path | Purpose |
| --- | --- |
| `rawledger/` (moved from `internal/rawledger/`) | Shared core, now an **exported** package so the source module can import it. Unchanged logic. |
| `source/` | New Go module (`…/source`, go 1.26.1) isolating all flowctl/gRPC weight from the lean origin. |
| `source/cmd/raw-ledger-source/main.go` | Env-driven wiring: backend config, `source.New`, register producer, signal-based shutdown. |
| `source/cmd/raw-ledger-source/produce.go` | Producer: parses range, prepares backend, drains ledgers through `rawledger.ToRawLedger`, marshals, emits `flowctlv1.Event`. Pure helpers `rangeFromParams` and `toEvent`. |
| `source/cmd/raw-ledger-source/produce_test.go` | Unit tests for `rangeFromParams` (6 cases) and `toEvent` (envelope + RawLedger payload round-trip). |
| `source/internal/backend/backend.go` | Backend selection seam. RPC wired; ARCHIVE / CAPTIVE_CORE are explicit, loud-failing stubs. |
| `Dockerfile.flowctl` | Container build (context = repo root, so `replace => ../` resolves). |

## Architecture

```
rawledger.ToRawLedger  (shared core, root module, no gRPC)
        │
        ├── processors/raw-ledger-origin   (nebu: JSONL on stdout)
        │
        └── source/cmd/raw-ledger-source   (flowctl service)
              backend.New() -> ledgerbackend.LedgerBackend
              GetLedger -> ToRawLedger -> proto.Marshal
              -> flowctlv1.Event{ type: "stellar.v1.RawLedger",
                                  content_type: "application/protobuf",
                                  payload: <marshalled RawLedger>,
                                  stellar_cursor: {ledger_sequence} }
              -> flowctl-sdk handles registration/heartbeat/health/gRPC
```

The flowctl `Event` is the generic envelope; the `RawLedger` rides in
`payload` as protobuf bytes. Because our `RawLedger` is wire/name-identical
to flow-proto's, a consumer decoding with `stellar.v1.RawLedger`
interoperates. `OutputEventTypes` advertises `stellar.v1.RawLedger`.

## Key decisions

1. **Two modules (chosen by the user).** Root stays `go 1.25`, gRPC-free,
   and its published `v0.1.0` origin is unaffected. `source/` is `go 1.26`
   and carries flowctl-sdk + flow-proto + gRPC. Trade-off accepted: more
   module ceremony for a genuinely lean origin.
2. **Core moved out of `internal/`.** Go's `internal/` is module-scoped,
   so the source module couldn't import it. `rawledger` is now a public
   package. (This is an API-surface change vs. the `v0.1.0` tag — see open
   issues.)
3. **Relative `replace => ../`** in `source/go.mod` for the shared core
   during development. No `go.work` committed; the replace makes
   `go build`/`go test`/`go mod tidy` work offline and self-contained.
4. **RPC backend only**, with the selection switch as the seam for ARCHIVE
   and CAPTIVE_CORE. Modeled on `stellar-live-source-datalake`.

## Verification performed

```
# source module (run inside: nix shell nixpkgs#go nixpkgs#gcc)
go vet ./...      # clean
go build ./...    # ok
go test ./...     # cmd/raw-ledger-source: ok (rangeFromParams, toEvent)

# runtime smoke (free ports)
HEALTH_PORT=39187 GRPC_PORT=39188 RPC_ENDPOINT=… raw-ledger-source
  -> "Health server started on port 39187"
  -> "Source raw-ledger-source started on :39188"
  -> GET /health => {"status":"HEALTHY","uptime":"2s",...}
  -> clean SIGINT shutdown ("Source raw-ledger-source stopped")

# root module unaffected by the core move
go test ./...     # rawledger: ok
nix build         # origin compiles; vendorHash unchanged; binary runs
```

## NOT yet done (next session / out of tracer-bullet scope)

1. **End-to-end stream against a live RPC + flowctl consumer.** Needs a
   real RPC endpoint and a flowctl control plane / consumer. The
   "bounded tracer-bullet run" from the proposal. Logic is unit-tested but
   not exercised against real ledgers.
2. **ARCHIVE + CAPTIVE_CORE backends.** Seams exist and fail loudly. Port
   from `stellar-live-source-datalake` (`createArchiveBackend`,
   `createCaptiveCore`) — these add datastore/captive-core deps.
3. **Replace `convertLedgerToProto` in `stellar-live-source-datalake`**
   with a call to this repo's `rawledger.ToRawLedger`, retiring its
   duplicate definition and the `RawLedgerChunk` vs `RawLedger` drift.
4. **Source nix package.** Not added to `flake.nix`: `buildGoModule`
   cannot follow the relative `replace => ../`. Options: switch to a
   tagged require + `go.work` for dev, or a custom derivation that uses
   the repo root as `src`. `Dockerfile.flowctl` covers containerization
   meanwhile.
5. **Heartbeat metrics richness.** The SDK sends heartbeats; we expose no
   custom per-ledger metrics yet (success/error/last-sequence counters
   like the datalake does). Wire via the SDK metrics hook if needed.

## Open issues / decisions

1. **Re-tag the root module.** `rawledger` moved out of `internal/` since
   `v0.1.0`. Tag a `v0.2.0` (public API change) so `source/go.mod` can
   eventually require it instead of the relative `replace`. Until then,
   the source module only builds in-tree.
2. **`git push --tags`** still pending for `nebu install` resolution (from
   M1) — unchanged.

## Cool-down note (Shape Up)

Milestone 2's tracer bullet hit its "Done" demo (service runs, health OK,
core reused) within appetite. Suggested cool-down before the next bet:
the live end-to-end run (#1 above) and the datalake de-duplication (#3),
which together retire the last copy of the conversion logic.
