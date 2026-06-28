# Handoff — Milestone 1: nebu-style `raw-ledger-origin`

**Status:** Complete and verified. Core tests pass, binary builds, the
`--describe-json` envelope is correct, and an end-to-end
`LedgerCloseMeta XDR | raw-ledger-origin | jq .` run emits the expected
JSONL.

## What was built

| Path | Purpose |
| --- | --- |
| `proto/stellar/v1/raw_ledger.proto` | Canonical schema. Messages-only (no gRPC service). Proto package `stellar.v1`, message `RawLedger`, fields 1–4 — identical to flow-proto. |
| `gen/stellar/v1/raw_ledger.pb.go` | Generated via nix `protoc`. Committed. |
| `internal/rawledger/event.go` | **Shared core.** `ToRawLedger(lcm, networkPassphrase) (*pb.RawLedger, error)`. Pure; no nebu/flowctl/Cobra imports. |
| `internal/rawledger/event_test.go` | Field population, XDR round-trip, empty-passphrase cases. |
| `internal/rawledger/raw_ledger_sync_test.go` | Wire-contract guard: fails if proto package/name/field numbers/kinds drift from flow-proto. |
| `processors/raw-ledger-origin/cmd/raw-ledger-origin/main.go` | nebu adapter. Owns the nebu coupling (`Emitter`, `RunProtoOriginCLI`). `origin` struct implements `cli.ProtoOriginProcessor[*pb.RawLedger]`. Path matches nebu's `nebu install` module-path derivation. |
| `description.yml` | nebu external-registry descriptor (see open issue below). |
| `flake.nix`, `Makefile` | Reproducible dev shell + codegen/build/test. |

## Key decisions (and why)

1. **Reuse flow-proto's `RawLedger`, but as a lean local copy.** flow-proto
   already defines `stellar.v1.RawLedger` (richer than the original
   proposal — it adds `network` and `closed_at`). Importing it directly
   would drag gRPC stubs and a `go 1.26.1` toolchain into a Unix-pipe
   JSONL binary. We instead own a **messages-only** copy that is
   wire/name-identical, guarded by a sync test. (Chosen by the user over
   direct import.)

2. **Core is a pure function, not the channel `Origin`.** The proposal
   sketched a channel-based `Origin{out chan}`. That shape fits nebu's
   push model but not the flowctl gRPC server. The genuine shared atom is
   `ToRawLedger`; the channel/`Emitter` wrapper lives in the nebu adapter
   (`cmd/`), keeping the core adapter-neutral and directly reusable by the
   M2 source.

3. **`closed_at` via `lcm.LedgerCloseTime()`**, `network` via the CLI
   helper's `networkPass`. Both surfaced for downstream mainnet/testnet
   disambiguation.

## Verification performed

```
go test ./...        # internal/rawledger: ok
go build ./...        # ok (needs gcc on PATH — nix provides it)
raw-ledger-origin --describe-json   # schema id nebu.raw_ledger.v1, type stellar.v1.RawLedger
<fixture XDR> | raw-ledger-origin - | jq .   # one RawLedger JSONL event, all fields populated
```

Run them with: `nix shell nixpkgs#gcc --command go test ./...` (or inside
`nix develop`).

## Resolved since initial handoff

1. **`nebu install` path mismatch — RESOLVED (Option 1).** The binary was
   moved to `processors/raw-ledger-origin/cmd/raw-ledger-origin/`, matching
   nebu's `external.ToProcessorEntry()` derivation
   (`github.com/<repo>/processors/<name>/cmd/<name>`). Default
   `nebu install raw-ledger-origin` now resolves with no override.

2. **nebu dependency — RESOLVED.** `go.mod` now requires the tagged
   `github.com/withObsrvr/nebu v0.6.7` (local `replace` removed).
   End-to-end output stamps `_nebu_version: v0.6.7`.

3. **`flake.nix` `vendorHash` — RESOLVED.** Set to
   `sha256-AqX75cU/I8ECirG2Sw6IIuw79r6mCTk14iHPIgBfV4U=`. `nix build`
   compiles the binary and it runs. To recompute after a dependency
   change: set `vendorHash = pkgs.lib.fakeHash`, run `nix build`, and copy
   the `got:` hash from the mismatch error.

4. **`.gitignore` bug — RESOLVED.** The binary-ignore patterns
   `raw-ledger-origin` / `raw-ledger-source` were unanchored and matched
   the identically-named *source* directories under `processors/`, so git
   silently skipped the binary's source (and `nix build` from the git tree
   then vendored without nebu, producing a wrong hash). Patterns are now
   anchored (`/raw-ledger-origin`). Lesson: keep all source files tracked
   in git before computing a flake `vendorHash`, since flakes build from
   the git tree.

## Open issues / decisions for next session

1. **Tag the repo `v0.1.0`** so `description.yml`'s `repo.ref` resolves
   for registry-driven `nebu install`. Nothing else blocks publishing.

## Milestone 2 readiness (flowctl `raw-ledger-source`)

Confirmed during this milestone:
- **flowctl-sdk** (`github.com/withObsrvr/flowctl-sdk`) provides a
  near-zero-boilerplate `source.New(cfg)` + `OnProduce(func) -> <-chan
  *flowctlv1.Event`, handling registration, heartbeats, health, gRPC, and
  shutdown.
- The source's `OnProduce` will: select a backend (RPC/archive/captive
  core), pull `LedgerCloseMeta`, call `rawledger.ToRawLedger`, marshal the
  result to bytes, and emit a `flowctlv1.Event{ type:
  "stellar.v1.RawLedger", content_type: "application/protobuf", payload:
  <marshaled RawLedger> }`.
- Because our `RawLedger` is wire-identical to flow-proto's, consumers
  decoding with flow-proto's type interoperate. `output_event_types`
  should advertise `stellar.v1.RawLedger`.
- The existing `stellar-live-source-datalake` is the reference for backend
  selection; its `convertLedgerToProto` should be replaced by a call to
  this repo's `rawledger.ToRawLedger` to kill the duplicate definition
  (and the existing `RawLedgerChunk` vs `RawLedger` drift).
- flowctl-sdk/flow-proto require `go 1.26.1`; the M2 binary will pull that
  toolchain. That's fine for a service (unlike the lean CLI), and it is
  isolated to `cmd/raw-ledger-source` + its own deps.
