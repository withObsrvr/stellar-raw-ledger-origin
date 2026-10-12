# Source terminal stream contract (flowctl-sdk v0.1.4)

`raw-ledger-source` registers `OnProduceStream`. A request with `end_ledger > 0`
completes successfully only after the entire contiguous range has been queued and
the upstream iterator has ended without error. An unbounded iterator returning
without cancellation fails. The source never skips a decode, sequence, or
upstream error. The SDK drains already-buffered events before returning the
terminal gRPC error; consumers must treat previously received events as
at-least-once and resume from their own durable cursor, not from a source-side
checkpoint. The SDK reports a downstream gRPC Send failure independently.

Startup invalid ranges map to `InvalidArgument`; upstream failures (including
unexpected unbounded EOF) to `Unavailable`; malformed XDR, gaps, duplicates,
and incomplete bounded ranges to `DataLoss`; envelope encoding errors to
`Internal`. Client/shutdown cancellation is not a source failure. The SDK
retains full error causes locally but returns safe status messages remotely.
Source error context includes a generated stream ID, backend type, requested
range, expected and last queued ledger, and operation. Structured source logs
include these fields and a stable error kind, but do not include endpoints or
credentials. SDK health is marked unhealthy on non-cancellation producer errors.
The upstream error cause is retained by the SDK for local debugging; do not put
credentials in RPC URLs or other upstream-generated error messages.

Service startup now validates backend configuration and numeric environment values
before binding listeners. `raw-ledger-source validate-config` performs offline
validation; `print-config --redact` prints a restricted, redacted summary, and
`version --json` prints the binary version. These commands do not claim remote
archive manifest or upstream connectivity validation. Invalid configuration
and usage exit 2. The SDK's `/live`, `/ready`, `/health`, and `/metrics` endpoints
exist, but `/metrics` currently returns SDK JSON rather than Prometheus output;
last fetched and emitted gauges, diagnostic stream snapshots, and separate
stall detection remain future work; this release records the last queued ledger
on terminal errors, not the last ledger the downstream consumer committed.

This change requires flowctl-sdk >= v0.1.4 and pins Stellar SDK v0.7.3.
The proposed component version is 0.2.4; it is **not published** until the
release commit is tagged and an immutable image digest is recorded. Existing `OnProduce` users retain
legacy channel-close semantics; rollback is to the previous source binary and
SDK pin. The flowctl pipeline supervisor still needs its separate forwarding
error propagation change before source errors reliably fail a *pipeline run*.
No automatic retries or durable checkpointing are performed here.

For diagnosis, inspect source health and the source's terminal error log, then
compare the ingester's durable watermark with the last ledger actually received.
Restart the consumer at its documented durable cursor, tolerating replay. Use
`go test ./...` and `go vet ./...` from `source/` for injected upstream,
continuity, cancellation, and bounded completion cases. A local process-driver
smoke with freshly built flowctl and processor binaries and the public testnet
RPC passed on 2026-10-12 (bounded 101-ledger request, sink received 101 contract
events). After bounded completion the current flowctl still reported all three
components healthy until timeout; flowctl's run-lifecycle fix remains a
separate repository change. Release tag, image digest, production rollout, and
operator dashboard remain pending.
