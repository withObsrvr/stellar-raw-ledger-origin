# Raw ledger source modernization proposal

**Status:** Proposed  
**Decision:** Make `stellar-raw-ledger-origin`—specifically its
`raw-ledger-source` adapter—the supported service going forward. Treat
`ttp-processor-demo/stellar-live-source-datalake` as a legacy compatibility
implementation and retire it after its consumers migrate.

## Repository plans

This document is the cross-repository roadmap and the source-specific plan.
Implementation details owned by other repositories live with their code:

| Repository | Local modernization document |
| --- | --- |
| `stellar-raw-ledger-origin` | `docs/raw-ledger-source-modernization-proposal.md` (this document) |
| `flowctl-sdk` | `/home/tillman/Documents/flowctl-sdk/docs/modernization-plan.md` |
| `flowctl` | `/home/tillman/Documents/flowctl/docs/modernization-plan.md` |
| `obsrvr-stellar-components` | `/home/tillman/Documents/obsrvr-stellar-components/docs/modernization-plan.md` |
| `flow-proto` | `/home/tillman/Documents/flow-proto/MODERNIZATION.md` |
| `ttp-processor-demo` | `/home/tillman/Documents/ttp-processor-demo/docs/modernization-migration-plan.md` |

The documents must remain consistent about ownership and rollout order. A PR
updates the plan in the repository whose public behavior changes; this roadmap
only needs an update when cross-repository ordering, ownership, or migration
policy changes.

## Executive summary

There are currently two implementations of the same broad responsibility:

- `stellar-live-source-datalake` is an older, custom gRPC service embedded in
  `ttp-processor-demo`. It has its own protobuf service, backend lifecycle,
  metrics, health server, flowctl registration, packaging, and documentation.
- `stellar-raw-ledger-origin` has one adapter-neutral conversion core and two
  runtime adapters: the nebu `raw-ledger-origin` CLI and the flowctl
  `raw-ledger-source` service. The service already supports RPC, archive, and
  Captive Core through the current Stellar SDK `LedgerStream` abstraction.

Continuing to modernize both services would preserve duplicate wire contracts,
backend code, configuration, packaging, tests, and operational failure modes.
The newer repository already embodies the desired architecture, so new work
should land there.

The first priority is not another backend or performance feature. It is making
failures unmistakable. Today a stream failure, conversion failure, or sequence
mismatch in `raw-ledger-source` is logged inside its producer goroutine and then
the output channel closes. The surrounding runtime and downstream operator may
only observe that data stopped. This matches the current operational experience
of restarting either the source or ingester without knowing which failed.

The target outcome is:

1. every stream has a visible lifecycle and stable identity;
2. terminal failures propagate to flowctl and health/readiness state;
3. the source exposes the last attempted and last successfully emitted ledger;
4. the ingester exposes the last received and last durably committed ledger;
5. dashboards and alerts identify which side stopped and why;
6. consumers resume from an explicit durable cursor with documented
   at-least-once semantics; and
7. the old datalake source can be removed without a flag day.

## Why `stellar-raw-ledger-origin` is the forward path

The newer repository already has the stronger foundation:

| Concern | `stellar-live-source-datalake` | `stellar-raw-ledger-origin` |
| --- | --- | --- |
| Conversion | Legacy service-local protobuf mapping | Shared `rawledger` core used by nebu and flowctl |
| Event schema | Older `raw_ledger_service.RawLedger` with sequence and XDR | Canonical `stellar.v1.RawLedger` with sequence, XDR, network, and close time |
| Acquisition API | Repeated `GetLedger` loop | Current SDK `LedgerStream` |
| Ranges | Unbounded only | Bounded and unbounded |
| Archive paths | GCS/S3 behavior differs | Shared bucket/prefix handling for GCS and S3 |
| Captive Core | Ignores the configured base config path | Supports base config and storage path |
| Lifecycle | Hand-written gRPC, health, and flowctl code | Delegated to `flowctl-sdk`, including graceful shutdown |
| Packaging | Conflicting ports and legacy defaults | Version-alignment check, Nix packages, flowctl image metadata |
| Tests | No service tests found | Core, schema guard, backend config, producer, packaging, and E2E smokes |
| nebu | None | Native `raw-ledger-origin` adapter |

The old service has also accumulated misleading surfaces: documented bounded
ranges that do not exist in its protobuf, advertised authentication/TLS that is
not implemented, nominal SLA constants that are not measured guarantees,
placeholder Protocol 23 validation, and inconsistent port defaults. Porting all
of nebu's quality-of-life work into that implementation would improve the wrong
long-term boundary.

## Architectural boundary

The desired ownership is:

```text
Stellar RPC / archive / Captive Core
                 │
                 ▼
        ledgerstream (acquisition)
                 │
                 ▼
       rawledger (canonical event)
          ┌──────┴────────┐
          ▼               ▼
 raw-ledger-origin   raw-ledger-source
   nebu / JSONL       flowctl / protobuf
                          │
                          ▼
                   durable ingester
```

The source should remain stateless with respect to consumer durability. It
serves a requested range and reports exactly what it emitted. The ingester owns
the durable checkpoint because only the ingester knows when a ledger and all of
its derived state have committed successfully.

On reconnect, the ingester requests the ledger following its durable cursor.
If batching or transaction boundaries make that unsafe, it requests from the
last durable ledger and deduplicates. The chosen replay rule must be written
down and tested as an at-least-once contract; neither component should silently
infer it.

## Priority 0: make failures observable and fatal

This is the work most likely to eliminate blind restarts.

### Propagate producer termination

Change the source/SDK boundary so asynchronous production can report a terminal
error, not only close a channel. Acceptable designs include an event/error pair,
an error channel, or an SDK stream object with a terminal `Err` result. The
exact API belongs in `flowctl-sdk`, but `raw-ledger-source` must not translate
these conditions into an apparently successful EOF:

- upstream stream error;
- malformed ledger XDR;
- unexpected ledger sequence or gap;
- protobuf/envelope encoding failure;
- downstream send failure;
- backend initialization failure after a request begins.

Cancellation caused by client disconnect or service shutdown is not a failure
and should be classified separately.

Until the SDK can propagate asynchronous errors, record terminal stream state
in health/metrics and emit one structured `ERROR` log with the stream ID,
backend, requested range, expected ledger, last emitted ledger, error class,
and full error chain.

### Add stream lifecycle state

Track each active stream with:

- stable stream/request ID;
- component and source ID;
- backend and network;
- requested start and end ledger;
- start time;
- current expected ledger;
- last fetched ledger;
- last emitted ledger;
- last emission time;
- state: `starting`, `working`, `caught_up`, `blocked`, `done`, `canceled`, or
  `error`;
- terminal error class and message.

Bounded streams become `done` only after the requested end ledger is emitted.
Unbounded streams may be `caught_up` while waiting at the network tip, but must
remain live. A closed unbounded stream without cancellation is an error.

### Replace unstructured logging

Use structured JSON logs in production and readable text locally. Every log
record should carry stable fields rather than requiring message parsing:

```text
component, version, stream_id, backend, network, start_ledger, end_ledger,
expected_ledger, ledger_sequence, last_emitted_ledger, error_kind
```

Never log `RPC_AUTH_HEADER`, cloud credentials, or complete request metadata
that may contain secrets.

### Add stable error classes

Use a small documented classification, for example:

- `configuration`
- `backend_connect`
- `backend_unavailable`
- `backend_timeout`
- `ledger_not_found`
- `ledger_gap`
- `invalid_xdr`
- `encoding`
- `downstream`
- `canceled`
- `internal`

These values should be stable in logs, metrics, health details, and flowctl
status. Human-readable messages may evolve.

## Priority 1: health, readiness, and metrics that answer “what failed?”

Expose separate endpoints:

- `/live`: the process is running and its internal monitor is responsive;
- `/ready`: configuration is valid, the service can accept a stream, and no
  process-wide fatal condition exists;
- `/health`: diagnostic JSON for humans and automation;
- `/metrics`: Prometheus exposition.

Do not make readiness depend on a client having requested a stream. Report
“ready, no active stream” explicitly.

Recommended bounded-cardinality metrics:

```text
raw_ledger_source_streams_active
raw_ledger_source_streams_total{result}
raw_ledger_source_ledgers_fetched_total{backend}
raw_ledger_source_ledgers_emitted_total{backend}
raw_ledger_source_stream_errors_total{backend,error_kind}
raw_ledger_source_last_fetched_ledger{backend,network}
raw_ledger_source_last_emitted_ledger{backend,network}
raw_ledger_source_last_emitted_timestamp_seconds{backend,network}
raw_ledger_source_fetch_duration_seconds{backend}
raw_ledger_source_emit_duration_seconds
raw_ledger_source_reconnects_total{backend}
```

Do not use stream IDs, ledger numbers, endpoints, or raw errors as metric
labels. Put those in logs and diagnostic health output.

The ingester needs complementary metrics:

```text
ingester_last_received_ledger
ingester_last_committed_ledger
ingester_last_commit_timestamp_seconds
ingester_commits_total{result}
ingester_source_reconnects_total{result}
```

With both sets, diagnosis becomes mechanical:

| Observation | Likely owner |
| --- | --- |
| Source last-emitted advances; ingester last-received does not | transport or ingester receive loop |
| Ingester last-received advances; last-committed does not | ingester transformation/storage |
| Source last-fetched does not advance and has a backend error | source/upstream backend |
| Source is caught up and close-time lag is small | healthy idle-at-tip state |
| Both advance but end-to-end lag grows | throughput/backpressure issue |

Add alerts for terminal stream error, no emission beyond an expected interval,
increasing ledger lag, commit lag, and repeated reconnect loops. “No emission”
must account for whether the network itself is producing new ledgers.

## Priority 2: operator quality of life

Bring the useful human and machine-facing principles from nebu to the service
without pretending a daemon is an interactive CLI.

### Add operational commands

Provide a small command surface:

```text
raw-ledger-source run
raw-ledger-source validate-config
raw-ledger-source diagnose
raw-ledger-source version --json
raw-ledger-source print-config --redact
```

`run` can remain the default for compatibility. `validate-config` must validate
backend-specific requirements and exit without opening listeners.
`diagnose` should perform bounded connectivity checks and report the selected
backend, network, archive schema/manifest compatibility, control-plane reachability,
and whether a requested sample ledger can be read. It must never print secrets.

Use stable exit codes:

- `0`: success;
- `2`: configuration or usage error;
- `3`: upstream/backend unavailable;
- `4`: protocol or data-integrity failure;
- `5`: downstream/control-plane failure;
- `130`: interrupted.

### Configuration behavior

- Validate every numeric environment value. The current parser silently falls
  back to defaults for invalid values; a typo such as `NUM_WORKERS=fast` should
  fail startup.
- Reject unknown backend types and incompatible settings before binding ports.
- Report where each effective value came from: explicit configuration, legacy
  alias, or default.
- Warn once for legacy aliases and document their removal release.
- Use canonical Stellar network names as convenience inputs while retaining an
  explicit passphrase override.
- Validate archive schema against the remote manifest before serving traffic.
- Publish a redacted effective configuration at startup and in `/health`.

### Status protocols

OSC 7501 is appropriate for the interactive `raw-ledger-origin` CLI and should
come through the current nebu processor helpers. It is not the primary status
channel for a headless service running in Docker or flowctl.

For `raw-ledger-source`, use flowctl lifecycle status, structured logs, health,
and Prometheus metrics. If an NDJSON status file is useful for a local process
driver, use the same stable status schema principles as nebu, but do not emit
terminal escape sequences into service logs.

## Priority 3: correctness and recovery contract

### Enforce contiguous delivery

The source already checks sequence continuity. Make this contractual:

- never skip a ledger after a fetch, decode, or encode error;
- never report bounded completion before the end ledger is emitted;
- identify duplicate delivery explicitly;
- terminate and require a resume from the durable cursor when continuity cannot
  be proven.

Add tests for a missing ledger, duplicate ledger, out-of-order ledger, stream
error, bounded completion, unbounded cancellation, downstream cancellation, and
an error on the final bounded ledger.

### Define retry ownership

Backend-internal transient retries may occur with bounded exponential backoff
and jitter. Once the source cannot guarantee backend stream state, it should
terminate the request rather than continue from an assumed sequence.

The ingester or flowctl supervisor owns reconnect and resume because it owns the
durable cursor. This avoids two retry loops masking each other.

Document:

- retryable versus terminal error classes;
- maximum attempts and backoff;
- when a fresh backend instance is created;
- how the resume ledger is selected;
- duplicate tolerance;
- shutdown and cancellation behavior.

### Add progress and stall detection

For an active unbounded stream, distinguish:

- fetching;
- caught up at tip;
- waiting on upstream;
- downstream backpressure;
- retrying;
- terminal failure.

A watchdog should compare last progress time with backend/network state. It
should report a stall and fail readiness when appropriate, but must not restart
the process blindly. Let the supervisor restart only after a classified terminal
failure or an explicit watchdog policy.

## Priority 4: compatibility migration

Many `ttp-processor-demo` components import the old generated
`raw_ledger_service` client directly. The new source is therefore the correct
future architecture but not yet a drop-in replacement.

### Inventory consumers

Create a checked migration inventory for at least:

- DuckLake ingestion variants;
- contract data/events/invocation processors;
- account balance processor;
- TTP processor;
- Stellar Arrow source;
- deployment manifests, pipeline templates, Nix files, Docker files, and docs.

For each consumer record its current proto package, source endpoint variable,
start/resume behavior, checkpoint owner, expected event type, and deployment.

### Migrate directly by default

The default strategy is to migrate every active consumer to the canonical
flowctl `stellar.ledger.v1` event API. Do **not** build a transition adapter in
advance merely because legacy client code exists in the repository.

Repository references are not evidence of a deployed dependency. First identify
the actual production and staging deployments, then classify each consumer:

- **migrate** — an active workload that will move to the canonical pipeline;
- **retire** — unused, superseded, or demo-only code that should not be ported;
- **blocked** — an active workload that cannot migrate within the cutover
  window, with a documented reason and owner.

Run the legacy and canonical pipelines in parallel during migration. Keep the
existing `stellar-live-source-datalake` deployment available, unchanged, as the
rollback target for the defined observation window. This provides rollback
without adding another protocol implementation.

### Conditional adapter decision gate

A legacy compatibility adapter is a contingency, not planned scope. It may be
approved only if the checked deployment inventory identifies at least one
**blocked active consumer** and one of these conditions applies:

- the consumer cannot be changed within the migration window;
- an external user depends on the legacy protobuf contract;
- deployments must move independently and parallel legacy/canonical sources are
  not operationally possible; or
- a tested production rollback requires the canonical source to serve an
  unchanged legacy ingester.

Before implementation, write a short decision record naming:

- the blocked deployment and business/operational reason;
- why parallel operation of the old source is insufficient;
- adapter owner;
- exact legacy methods and fields supported;
- release in which the adapter is introduced;
- measurable removal condition;
- expiration date or release; and
- tests proving it cannot become a second acquisition implementation.

If approved, implement the legacy gRPC surface as a thin adapter over the same
`ledgerstream` and `rawledger` core. Preserve required old field numbers, mark
the surface deprecated, and do not copy `stellar-live-source-datalake` backend,
retry, health, or conversion code. If no blocked active consumer is found, close
the decision gate with “adapter not required” and proceed with direct migration.

### Prove parity before cutover

Run both sources against the same bounded archive range and compare:

- exact sequence set and ordering;
- SHA-256 of every `ledger_close_meta_xdr` payload;
- network and close-time fields;
- behavior on cancellation and injected failures;
- sustained throughput and memory;
- restart from the ingester's durable checkpoint.

Then shadow the new source in production without writing to the authoritative
sink. Cut over one consumer, retain the old deployment as a rollback target for
a defined observation window, and only then migrate the remainder.

## Priority 5: release and supportability

- Update both Go modules to supported, intentionally aligned versions of nebu,
  `go-stellar-sdk`, `flowctl-sdk`, and `flow-proto`.
- Remove the development-only relative module replacement from released source
  builds by tagging the shared root module and pinning that tag.
- Generate a software bill of materials and publish signed images with immutable
  version tags; do not deploy `latest`.
- Add CI for root and source tests, vet, packaging alignment, Nix builds,
  container layout, protobuf compatibility, and bounded archive/RPC integration.
- Publish a single runbook covering configuration, startup, dashboards, alerts,
  failure classes, resume procedure, and rollback.
- Add a compatibility policy for the event schema and component configuration.

## Proposed pull-request sequence

Keep each PR independently testable and deployable.

1. **Failure propagation contract**
   - Add source terminal-error propagation, error classes, and tests.
   - If required, make the minimal corresponding `flowctl-sdk` change first.

2. **Structured lifecycle logging**
   - Add stream IDs, lifecycle transitions, redaction, and correlation fields.

3. **Meaningful health and Prometheus metrics**
   - Add live/ready/health/metrics and source progress metrics.

4. **Strict configuration and diagnostic commands**
   - Add `validate-config`, `diagnose`, `version --json`, redacted config, and
     stable exit codes.

5. **Recovery semantics**
   - Document and test contiguous delivery, retry ownership, bounded completion,
     cancellation, and durable-cursor resume with a reference ingester.

6. **Consumer migration, one component per PR**
   - Move each active ingester/processor and its deployment documentation.

7. **Production shadow and cutover tooling**
   - Add parity comparison, lag dashboards, alerts, rollback, and runbook.

8. **Conditional compatibility adapter, only if approved**
   - Implement only when the migration inventory identifies a blocked active
     consumer and the decision record satisfies the adapter gate.

9. **Retire `stellar-live-source-datalake`**
   - Mark deprecated, remove it from active pipeline templates and builds, then
     archive/delete only after the observation window and rollback deadline.

## Definition of done

The migration is complete when:

- one supported raw-ledger acquisition implementation owns RPC, archive, and
  Captive Core behavior;
- both nebu and flowctl adapters use the same canonical conversion core;
- any source or ingester failure is visible in structured status within one
  monitoring interval;
- operators can identify the failing component and last safe ledger without
  restarting anything;
- bounded streams are proven contiguous and complete;
- unbounded streams distinguish caught-up, stalled, canceled, and failed;
- restart/resume from the ingester's durable cursor is integration-tested;
- every active consumer uses the canonical source; if a temporary compatibility
  adapter was approved, its remaining consumers, owner, removal condition, and
  expiration are documented;
- production images are versioned and reproducible; and
- `stellar-live-source-datalake` is no longer required by an active deployment.

## Developer handoff

This section is the starting point for an engineer implementing the proposal.
Read it before changing code. Paths describe the machine on which this proposal
was prepared on 2026-10-12; verify them with `git status`, `git remote -v`, and
`git log -1` before beginning because the checkouts may have advanced.

### Local repository map

| Responsibility | Local path | Upstream | State observed while writing |
| --- | --- | --- | --- |
| Canonical raw-ledger core, nebu adapter, and flowctl source | `/home/tillman/Documents/stellar-raw-ledger-origin` | `git@github.com:withObsrvr/stellar-raw-ledger-origin.git` | `main` at `a92ba11`, tagged `v0.2.3`; this proposal is an uncommitted new file |
| Canonical processor, sinks, DuckLake ingest, and backfill workers | `/home/tillman/Documents/obsrvr-stellar-components` | `git@github.com:withObsrvr/obsrvr-stellar-components.git` | clean `main` at `bc0560b` |
| Legacy source and direct-gRPC consumers | `/home/tillman/Documents/ttp-processor-demo` | `git@github.com:withObsrvr/ttp-processor-demo.git` | clean `main` at `114e0c93` |
| nebu CLI/runtime reference implementation | `/home/tillman/Documents/nebu` | `git@github.com:withObsrvr/nebu.git` | feature branch was checked out; do not build unrelated source work on that branch |
| Generic component runtime SDK | `/home/tillman/Documents/flowctl-sdk` | `git@github.com:withObsrvr/flowctl-sdk.git` | clean `main` at `6ee5ac5` |
| Pipeline supervisor and stream orchestrator | `/home/tillman/Documents/flowctl` | `git@github.com:withObsrvr/flowctl.git` | clean `main` at `63cefb0` |
| Canonical flowctl and Stellar protobuf contracts | `/home/tillman/Documents/flow-proto` | `git@github.com:withObsrvr/flow-proto.git` | clean `main` at `cf509a6` |

All repositories needed for the first failure-propagation workstream are now
available as sibling checkouts under `/home/tillman/Documents`. Do not edit
released module copies under `/home/tillman/go/pkg/mod`; those are read-only
build inputs, not development checkouts.

The commit identifiers above are observations, not pins. Before starting:

```bash
for repo in \
  stellar-raw-ledger-origin \
  obsrvr-stellar-components \
  ttp-processor-demo \
  flowctl-sdk \
  flowctl \
  flow-proto; do
  git -C "/home/tillman/Documents/$repo" status --short
  git -C "/home/tillman/Documents/$repo" branch --show-current
  git -C "/home/tillman/Documents/$repo" log -1 --oneline
done
```

Do not pull/rebase a dirty checkout until its changes are understood. This
proposal itself is currently an untracked file in `stellar-raw-ledger-origin`.

### Repository ownership and change routing

Use these rules to decide where a change belongs:

| Change | Owning repository |
| --- | --- |
| Convert LedgerCloseMeta/XDR into canonical `stellar.v1.RawLedger` | `stellar-raw-ledger-origin/rawledger` |
| Configure RPC, archive, or Captive Core acquisition | `stellar-raw-ledger-origin/source/ledgerstream` |
| Raw source stream lifecycle and source-specific telemetry | `stellar-raw-ledger-origin/source/cmd/raw-ledger-source` |
| Generic producer/consumer error propagation or health semantics | `flowctl-sdk` |
| Pipeline supervision, process exit/restart, or component wiring | `flowctl` |
| Canonical protobuf fields or event envelopes | `flow-proto` first, followed by consumers |
| Stellar XDR extraction and normalized batch construction | `obsrvr-stellar-components/components/stellar-ledger-processor` |
| Durable commit, watermark, replay, or sink telemetry | the relevant sink/server in `obsrvr-stellar-components` |
| Historical high-throughput raw acquisition into Parquet | `obsrvr-stellar-components/cmd/ducklake-backfill-worker` and `internal/backfillworker` |
| Temporary compatibility for old direct gRPC clients | preferably a thin adapter in `stellar-raw-ledger-origin`; consumer migration remains in `ttp-processor-demo` |
| Critical legacy bug during migration | `ttp-processor-demo/stellar-live-source-datalake`, with no new independent architecture |

Do not move source acquisition into `obsrvr-stellar-components`, and do not move
DuckLake checkpoint ownership into the raw source. `obsrvr-stellar-components`
imports the public `ledgerstream` package for bounded historical workers; that
reuse is intentional and does not make it the source implementation owner.

### Current dependency baseline

Before the first PR, inspect and intentionally reconcile these observed pins:

| Module | nebu | Stellar SDK | flowctl SDK | flow-proto |
| --- | --- | --- | --- | --- |
| `stellar-raw-ledger-origin` root | `v0.6.7` | `v0.7.1` | n/a | local message copy guarded by tests |
| `stellar-raw-ledger-origin/source` | n/a | `v0.7.1` | `v0.1.1` | `v0.1.3` |
| `obsrvr-stellar-components` | n/a | `v0.7.1` | `v0.1.3` | `v0.1.3` |

The current nebu release on this machine is `v0.6.15`, and related processor
work has moved to Stellar SDK `v0.7.3`. Do not combine dependency upgrades with
the first failure-propagation behavior change unless compilation requires it.
First establish a green baseline, then use a dedicated dependency PR with
release notes and cross-repository integration tests.

`stellar-raw-ledger-origin/source/go.mod` currently uses a relative
`replace github.com/withObsrvr/stellar-raw-ledger-origin => ../`. Preserve it for
local development until the release process deliberately replaces it with a
tagged dependency. Do not publish a source module whose build accidentally
depends on a developer's parent directory.

### Files to read before implementation

In `stellar-raw-ledger-origin`:

1. `README.md` — supported adapters, schema, backend configuration, and build.
2. `rawledger/event.go` — adapter-neutral conversion boundary.
3. `rawledger/raw_ledger_sync_test.go` — wire compatibility guard.
4. `source/cmd/raw-ledger-source/main.go` — service wiring and shutdown.
5. `source/cmd/raw-ledger-source/produce.go` — range parsing and the current
   asynchronous producer failure behavior.
6. `source/cmd/raw-ledger-source/produce_test.go` — producer test seams.
7. `source/ledgerstream/stream.go` — backend construction and configuration.
8. `source/ledgerstream/stream_test.go` — backend/config coverage.
9. `metadata.json`, `Dockerfile.flowctl`, and `docs/release.md` — packaging and
   version alignment.
10. `scripts/flowctl-process-e2e.sh` and `scripts/flowctl-registry-e2e.sh` —
    existing end-to-end harnesses.

In `obsrvr-stellar-components`:

1. `docs/current-state.md` — authoritative topology and open production gates.
2. `docs/architecture.md` and `docs/event-contracts.md` — ownership and wire
   boundaries.
3. `docs/production-hardening-plan.md` — known delivery and health failures.
4. `docs/flowctl-sdk-upgrade-plan.md` — already identified SDK work; reconcile
   rather than create a competing SDK plan.
5. `components/stellar-ledger-processor/cmd/component/main.go` — source event
   consumption and normalization entry point.
6. `components/ducklake-sink/cmd/component/main.go` — sink event handling.
7. `components/ducklake-sink/README.md` — commit, watermark, health, and replay
   contract.
8. `pipelines/local-archive-ducklake-flowctl.yaml` and
   `pipelines/local-archive-ingest-rpc-flowctl.yaml` — runnable pipeline shapes.
9. `scripts/quack-chaos-harness.sh`, `scripts/ducklake-crash-recovery-gate.sh`,
   and telemetry/checkpoint gates — established failure-injection patterns.

In the legacy repository, read only enough to inventory/migrate live consumers:

1. `stellar-live-source-datalake/go/server/server.go` and its protobuf.
2. `ducklake-ingestion-processor`, `ducklake-ingestion-obsrvr`, and
   `ducklake-ingestion-obsrvr-v2` source clients.
3. contract/account/TTP processor copies of `raw_ledger_service.proto`.
4. production manifests outside the repository that still name
   `stellar-live-source-datalake`.

In `flowctl-sdk`:

1. `README.md` — public component-author API and examples.
2. `pkg/source/source.go` — `ProducerFunc`, `StandardSource`, and
   `SourceService.StreamEvents`.
3. `pkg/flowctl/health.go` and metrics/controller implementations — existing
   lifecycle state and extension points.
4. source, consumer, and processor tests — preserve compatibility for all
   component types, not just raw ledgers.

In `flowctl`:

1. `internal/runner/stream_orchestrator.go` — every forwarding goroutine and
   current error-loss boundary.
2. `internal/runner/pipeline_runner.go` — run lifecycle, wait, and shutdown.
3. `internal/runner/monitor.go` — process/component monitoring.
4. process-orchestrator code under `internal/orchestrator` — child exit status
   and restart behavior.
5. `cmd/run.go` — final CLI exit behavior.

In `flow-proto`, identify the canonical source service and health/status
messages before proposing a schema change. Prefer solving terminal propagation
with existing gRPC error status and SDK types. Change protobuf only if the
required semantics cannot be represented without it, because a proto release
forces coordinated regeneration and dependency updates across every component.

### First implementation workstream: terminal source failures are visible

This work crosses three ownership boundaries and should normally be three
ordered PRs, not one cross-repository change:

1. `flowctl-sdk`: add a backward-compatible error-aware producer contract and
   make `SourceService.StreamEvents` return terminal producer errors over gRPC.
2. `stellar-raw-ledger-origin`: adopt that contract and classify producer
   termination correctly.
3. `flowctl`: propagate source/processor/sink forwarding failures to the
   pipeline run instead of logging them inside detached goroutines.

Each PR should be narrowly scoped. The workstream goal is:

> A non-cancellation failure after a stream starts cannot appear to flowctl or
> an operator as successful producer completion.

#### Evidence of the current problem

There are two independent error-loss boundaries:

1. `stellar-raw-ledger-origin/source/cmd/raw-ledger-source/produce.go` creates a
   buffered event channel and a goroutine. On an upstream stream error,
   conversion failure, sequence mismatch, or envelope encoding failure, that
   goroutine logs and returns. Its deferred `close(out)` is the only signal
   received by the SDK. The producer function has already returned successfully,
   so the terminal error is not returned to its caller.
2. `flowctl/internal/runner/stream_orchestrator.go` forwards gRPC streams in
   detached goroutines. Source `Recv`, processor `Recv`, processor `Send`, and
   sink `Send` errors are generally logged and followed by `return` or
   `continue`; they are not delivered to `PipelineRunner`. `WireAll` reports
   success once goroutines are launched, not when delivery completes.

Fixing only the first boundary will make the gRPC source stream return an error,
but current flowctl will still log that `Recv` error and let the pipeline remain
apparently alive. Both boundaries must be fixed before the operator can rely on
pipeline status.

Do not “fix” this only by adding another log line. The runtime must receive a
machine-readable failure signal.

#### Confirmed SDK contract

The local `flowctl-sdk` checkout currently defines:

```go
type ProducerFunc func(
    ctx context.Context,
    request *flowctlv1.StreamRequest,
) (<-chan *flowctlv1.Event, error)
```

The implementation is in `flowctl-sdk/pkg/source/source.go`. The returned error
only covers synchronous producer startup. `StandardSource.StreamEvents` treats
every later event-channel close as successful completion and returns `nil`.
There were no source package tests covering this behavior when inspected.

The receiving path is
`flowctl/internal/runner/stream_orchestrator.go:wireSourceToProcessor`. It treats
gRPC `io.EOF` as normal completion, but any other `Recv` error is only logged in
a goroutine. Similar error loss exists in processor chaining and sink fan-out.

Before coding, verify these paths have not changed and add characterization tests
that fail for the current behavior. The PR description should include the actual
call path and explain how a source error becomes a failed pipeline run.

#### Preferred contract

For the SDK, prefer a stream result that carries events and exactly one terminal
result, for example conceptually:

```go
type ProduceStream struct {
    Events <-chan *flowctlv1.Event
    Done   <-chan error
}
```

The exact shape may differ to fit the SDK. Required semantics are:

- `nil` terminal error is valid only for completed bounded ranges;
- `context.Canceled` is classified as canceled, not failed;
- unbounded source EOF without cancellation is an error;
- a terminal error is observed after already-buffered events are either sent or
  deliberately discarded according to one documented rule;
- the gRPC status sent to the consumer preserves retryability classification;
- flowctl records the component/run failure and the process exits nonzero or is
  otherwise marked unhealthy according to its supervision contract;
- there is no goroutine leak if the consumer disconnects before draining events.

If changing `ProducerFunc` would be an unacceptable immediate breaking change,
add a parallel error-aware API and migrate `raw-ledger-source` first. Mark the
old API deprecated and define its removal release; do not silently reinterpret
ordinary channel close globally.

A likely compatible shape is to retain `OnProduce(ProducerFunc)` and add an
`OnProduceStream(ErrorAwareProducerFunc)` registration method. Reject configuring
both handlers. `StreamEvents` can then support old producers unchanged while new
producers return explicit completion. Naming is illustrative; follow the SDK's
existing API conventions after reviewing the whole package.

The SDK must translate classified terminal failures to appropriate gRPC status
codes rather than returning an untyped formatted error. At minimum distinguish
invalid request/configuration, cancellation, unavailable upstream, integrity
failure, and internal failure. Preserve the full cause in server logs without
leaking secrets; return a safe message to remote clients.

#### Source-side implementation

Refactor the producer loop into a testable function that returns a classified
terminal error. Preserve these existing correctness properties:

- requested ranges are validated before starting acquisition;
- bounded ranges use `ledgerbackend.BoundedRange`;
- unbounded ranges use `ledgerbackend.UnboundedRange`;
- borrowed XDR is fully marshalled into an owned event before advancing;
- every emitted sequence equals the expected sequence;
- cancellation stops promptly.

Add stable error wrapping with enough context to identify backend, range,
expected sequence, and operation without including credentials.

#### Required tests

At minimum, table-drive these cases in the source module:

1. bounded range emits every sequence and completes successfully;
2. upstream error before the first ledger is propagated;
3. upstream error after one or more events is propagated;
4. invalid XDR is propagated;
5. out-of-order sequence is propagated as `ledger_gap`;
6. final bounded ledger encoding/send failure is propagated;
7. client cancellation is reported as canceled and does not leak a goroutine;
8. unbounded stream ending without cancellation is an error;
9. downstream stops reading while the producer is blocked on a full buffer;
10. terminal result is delivered exactly once.

Add SDK tests proving the source handler observes that error and exposes the
chosen gRPC/health status. Add a flowctl process-driver integration test proving
the pipeline run does not report success when a synthetic source emits one event
and then fails.

For flowctl, introduce one orchestrator-owned terminal error path instead of
adding unrelated ad hoc channels to each forwarding goroutine. A suitable design
is an `errgroup.WithContext` or equivalent first-error-wins mechanism owned by
`StreamOrchestrator`, with these properties:

- the first non-cancellation forwarding failure records component and edge;
- it cancels all other forwarding loops;
- all goroutines exit and are waited for;
- `PipelineRunner` receives the error and marks/exits the run as failed;
- expected bounded completion remains success;
- an unbounded source ending with EOF is failure unless the run was canceled;
- sink backpressure timeout is a failure, never a dropped event followed by
  continued success;
- sink `Send` and `CloseAndRecv` errors are returned, never counted and ignored;
- no pipeline can remain registered as healthy after its only stream goroutine
  has exited.

This is broader than the source-specific SDK PR, so keep it in the separate
flowctl PR and add focused orchestration tests before changing behavior.

#### First-workstream non-goals

Do not include:

- Prometheus metrics;
- CLI subcommands;
- dependency upgrades unrelated to the contract;
- consumer migrations;
- TLS/authentication;
- automatic checkpointing in the source;
- retry loops in both source and supervisor;
- deletion of the legacy source.

#### Verification commands

Run commands from the appropriate repository and report exact failures. Do not
hide network-dependent failures.

```bash
# Canonical source: root module
cd /home/tillman/Documents/stellar-raw-ledger-origin
go test ./...
go vet ./...
make packaging-check

# Canonical source: service module
cd /home/tillman/Documents/stellar-raw-ledger-origin/source
go test ./...
go vet ./...
go build ./...

# Existing process-driver smoke after SDK/source changes
cd /home/tillman/Documents/stellar-raw-ledger-origin
RUN_TIMEOUT=45s scripts/flowctl-process-e2e.sh

# Runtime SDK
cd /home/tillman/Documents/flowctl-sdk
go test ./...
go vet ./...

# Supervisor/orchestrator
cd /home/tillman/Documents/flowctl
go test ./...
go vet ./...

# Downstream regression suite
cd /home/tillman/Documents/obsrvr-stellar-components
go test ./...
go vet ./...
scripts/validate-pipelines.sh
```

When flowctl-sdk or flowctl changes, also run their repository-native test and
vet targets discovered from their README/Makefile. Do not invent commands before
cloning and reading their agent/repository instructions.

### Second implementation PR: correlated progress telemetry

After terminal errors propagate, add source progress telemetry in
`stellar-raw-ledger-origin` and verify complementary sink telemetry in
`obsrvr-stellar-components`.

Use one correlation vocabulary across logs and diagnostics:

```text
pipeline_id
run_id
component_id
stream_id
backend
network
requested_start_ledger
requested_end_ledger
expected_ledger
last_fetched_ledger
last_emitted_ledger
last_received_ledger
last_committed_ledger
```

Not every component knows every field. It must report what it owns and preserve
correlation identifiers from the event/request. The source must not claim a
commit; the sink must not claim a fetch.

Acceptance test:

1. run a bounded local archive pipeline for at least ten ledgers;
2. inject a source failure after ledger N and show source terminal state at N;
3. inject a sink failure after receiving N and before committing it;
4. show diagnostics identify the failing component and distinguish received
   from committed;
5. restart from the last durable watermark;
6. prove the final catalog has the exact requested range with zero gaps and no
   divergent duplicates.

Use the DuckLake `ingest_watermarks` gap query already documented in
`components/ducklake-sink/README.md` as the storage proof.

### Legacy consumer inventory and adapter decision template

Before considering a compatibility adapter, create an inventory using this
table. One row is required per deployed consumer, not merely per directory.

| Deployment | Status | Source binary/image | Consumer binary/image | Proto/API | Endpoint config | Start ledger source | Durable checkpoint owner | Restart policy | Migration owner | Target date/blocker |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| example | migrate | `stellar-live-source-datalake:<digest>` | `stellar-postgres-ingester:<digest>` | legacy `RawLedgerService` | `SOURCE_ENDPOINT` | consumer config | Postgres watermark | Nomad | unassigned | date required |

Search the local legacy repository with:

```bash
cd /home/tillman/Documents/ttp-processor-demo
rg -n --glob '!**/vendor/**' \
  'stellar-live-source-datalake|RawLedgerService|raw_ledger_service|SOURCE_ENDPOINT|SOURCE_SERVICE_ADDRESS'
```

Repository references do not prove a production deployment. Cross-check Nomad,
Docker Compose, Kubernetes, or other infrastructure repositories before calling
a consumer active or safe to retire.

After inventory, record exactly one adapter decision:

```text
Decision: adapter not required
Reason: every active consumer has an owned direct-migration plan; the existing
legacy deployment remains the rollback target through <date/release>.
```

or:

```text
Decision: temporary adapter approved
Blocked deployment: <name>
Reason parallel legacy operation is insufficient: <reason>
Owner: <person/team>
Removal condition: <measurable condition>
Expires: <date/release>
```

### Local development safety

- Use bounded ledger ranges for every integration test unless a test explicitly
  verifies cancellation of an unbounded stream.
- Prefer the public S3 archive for deterministic historical tests and RPC only
  for a small recent/tip smoke.
- Never place RPC tokens or cloud credentials in pipeline YAML, test fixtures,
  command history copied into docs, or PR descriptions.
- Do not write test data into a production DuckLake/Postgres destination.
- Use unique temporary ports and temporary catalogs so parallel tests cannot
  attach to or corrupt an existing catalog.
- Before a destructive reset or production backfill, provide the execution
  sequence, rollback point, and verification command and wait for confirmation.

### Documentation deliverables with each PR

Each behavioral PR should update this proposal or replace its completed section
with durable documentation covering:

- public contract and compatibility impact;
- configuration and defaults;
- error/status behavior;
- operator diagnosis steps;
- test and failure-injection evidence;
- rollout and rollback;
- version/tag/image digest deployed;
- remaining gaps.

Do not copy the old API README's unimplemented security or SLA claims. Document
only behavior demonstrated by code and tests.

## Immediate recommendation

Do not begin by rewriting `stellar-live-source-datalake` health metrics or CLI
surface. Start PR 1 in `stellar-raw-ledger-origin` and make producer failure
propagation observable end to end. In parallel, identify the production
ingester's durable cursor and expose its last-received/last-committed ledgers.
Those two changes will answer the current operational question—source or
ingester—while building on the implementation intended to replace the legacy
service.
