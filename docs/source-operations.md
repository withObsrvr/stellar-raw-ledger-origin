# Raw-ledger-source operations (0.2.5 candidate)

The source owns acquisition and protobuf conversion, **not consumer durability**.
Start from the ingester's last *committed* ledger (or replay the last committed
ledger with deduplication if the sink needs it). Treat every received ledger as
at-least-once. No source-side checkpoint or transparent reconnect is performed.

## Endpoints

- `HEALTH_PORT` (default `8088`): flowctl-sdk `/live`, `/ready`, `/health`,
  `/metrics`. The SDK's `/metrics` is **JSON**, not Prometheus.
- `METRICS_PORT` (default `9095`): source `/metrics` (Prometheus) and `/status`
  (diagnostic JSON). Keep this port internal to the cluster. `/status` shows
  `active` and `last` stream records; `null` active means no request is running.
  The last record is volatile and resets on restart. No credentials or full
  upstream error strings are included. Stream IDs appear in diagnostics/logs
  only, never as metric labels.

The `last_fetched_ledger` value means XDR was validated; `last_queued_ledger`
means an event was submitted to the SDK's channel. Neither establishes that
the client received it or that the ingester committed it. Check the SDK's gRPC
status for send errors and the ingester's durable watermark for safe restart.

Metrics include `raw_ledger_source_streams_active`,
`raw_ledger_source_streams_total{result}`,
`raw_ledger_source_stream_errors_total{backend,error_kind}`,
`raw_ledger_source_ledgers_fetched_total{backend}`,
`raw_ledger_source_ledgers_queued_total{backend}`,
`raw_ledger_source_last_fetched_ledger{backend}`,
`raw_ledger_source_last_queued_ledger{backend}`, and
`raw_ledger_source_last_queued_timestamp_seconds{backend}`. Label values are
fixed backend names, SDK error kinds, and `done`/`error`/`canceled` results.
Gauges are process-local and cannot be used as a durable ledger cursor.

Alert on an increase in `raw_ledger_source_stream_errors_total` and on source
lag **only when an independent network-tip signal shows new ledgers**. A quiet
unbounded stream may simply be caught up. Compare last fetched vs last queued,
then source last queued vs ingester last received and last committed to identify
which component stopped. The source does not currently distinguish upstream
waiting-at-tip from a stall; no watchdog changes SDK readiness. Do not alert
on absence of emitted ledgers alone.

## Diagnosing a failure

1. Fetch the SDK `/health` and `/ready`, then the source `/status`. Find
   `stream_id`, backend, requested range, `expected_ledger`, last fetched,
   last queued, state, and terminal error kind.
2. Search source logs by `stream_id`. SDK gRPC errors report classified status
   (`Unavailable`, `DataLoss`, `Internal`, etc.). Avoid putting secrets in URLs
   or request metadata: upstream libraries may include these in error chains.
3. Compare with the ingester's last received and last durably committed ledger.
   Do not infer commit from source counters.
4. Fix the classified cause. Have the consumer request its documented replay
   range from the durable watermark; verify contiguity and deduplication.
5. Roll back by immutable image digest if the new image is responsible. The
   published prior release digest is recorded in `docs/release.md`.

`validate-config` performs offline validation; it does not check network
reachability or archive manifests. Source and root tests, Nix builds, and
packaging alignment must pass before a release; the SDK-to-flowctl process
smoke uses `scripts/flowctl-process-e2e.sh` with bounded testnet ledgers.
