# flowctl raw-ledger-source completion plan

This is the working list to make `source/cmd/raw-ledger-source` a full replacement for `stellar-live-source-datalake` in flowctl pipelines.

## Current status

- `raw-ledger-source` exists in the `source/` Go module.
- RPC, ARCHIVE, and CAPTIVE_CORE backends build and unit-tests pass.
- Service lifecycle is delegated to `flowctl-sdk`.
- Shared conversion uses `rawledger.ToRawLedger`, same as the nebu origin.

## Ordered work

1. **Fix component ID registration** — done.
   - flowctl injects `FLOWCTL_COMPONENT_ID` from the pipeline component id.
   - Source now prefers `FLOWCTL_COMPONENT_ID`, then `SOURCE_ID`, then `raw-ledger-source`.

2. **Fix event type compatibility** — done.
   - Existing flowctl Stellar processors route on `stellar.ledger.v1`.
   - Source now advertises/emits `stellar.ledger.v1`.
   - Payload remains the protobuf `stellar.v1.RawLedger` message.

3. **Run local end-to-end with process driver** — done.
   - Added `scripts/flowctl-process-e2e.sh`.
   - The script builds `raw-ledger-source`, generates a temp smoke sink, and runs `raw-ledger-source -> contract-events-processor -> smoke-sink` over testnet RPC with the process driver.
   - Verified locally: `RUN_TIMEOUT=35s scripts/flowctl-process-e2e.sh` ended by timeout as expected and reported `Smoke passed: sink received 100 event(s).`

4. **Fix Docker image layout and metadata** — done.
   - `Dockerfile.flowctl` now copies the source binary to `/app/component`, one of the resolver-supported paths.
   - The image copies `metadata.json` to `/metadata.json`.
   - Metadata declares type `source`, produced event type `stellar.ledger.v1`, and config-to-env mappings for backend, RPC, ledger range, ports, control-plane, and component-id settings.
   - Added flowctl/OCI labels for component type, API version, source, version, license, and capabilities.

5. **Run end-to-end through normal flowctl registry/image path** — done.
   - Added `scripts/flowctl-registry-e2e.sh`.
   - The script builds the flowctl image, primes flowctl's OCI cache at the resolver's expected `docker.io/withobsrvr/raw-ledger-source:0.1.0` cache path, clears the extracted/component cache, and runs a pipeline whose source is declared as `type: raw-ledger-source@0.1.0` with config keys instead of an explicit command/env block.
   - flowctl resolves the type reference, extracts `/app/component` and `/metadata.json`, copies them to `~/.flowctl/components/raw-ledger-source/0.1.0/`, translates config through metadata `env_mapping`, and runs `raw-ledger-source -> contract-events-processor -> smoke-sink` over testnet RPC.
   - Verified locally: `RUN_TIMEOUT=45s scripts/flowctl-registry-e2e.sh` ended by timeout as expected and reported `Registry smoke passed: sink received 100 event(s); cached raw-ledger-source@0.1.0 with metadata type=source produces=stellar.ledger.v1.`

6. **Add ARCHIVE backend** — done.
   - Ported archive/datalake backend construction into `source/internal/backend` using Stellar SDK `datastore.NewDataStore` plus `ledgerbackend.NewBufferedStorageBackend`.
   - Supports `ARCHIVE_STORAGE_TYPE=GCS|S3`, `ARCHIVE_BUCKET_NAME`, optional `ARCHIVE_PATH`, archive schema knobs `LEDGERS_PER_FILE` / `FILES_PER_PARTITION`, and buffering knobs `BUFFER_SIZE` / `NUM_WORKERS`.
   - Supports legacy aliases `STORAGE_TYPE`, `BUCKET_NAME`, and `BUCKET_PATH`; `STORAGE_TYPE=GCS|S3` selects `BACKEND_TYPE=ARCHIVE` when `BACKEND_TYPE` is unset.
   - S3 uses `AWS_REGION` and optional `S3_ENDPOINT_URL`; both GCS and S3 pass a combined `destination_bucket_path` so bucket prefixes work.
   - Added backend unit tests for archive env parsing, legacy alias handling, bucket-prefix joining, and validation errors.
   - Updated `metadata.json` and README config docs with archive env mappings.

7. **Add CAPTIVE_CORE backend** — done.
   - Ported Captive Core backend construction into `source/internal/backend` using Stellar SDK `ledgerbackend.NewCaptiveCoreToml` / `NewCaptiveCoreTomlFromFile` plus `ledgerbackend.NewCaptive`.
   - Supports `STELLAR_CORE_BINARY_PATH`, `HISTORY_ARCHIVE_URLS`, `NETWORK_PASSPHRASE`, optional `STELLAR_CORE_CONFIG_PATH`, and optional `CAPTIVE_CORE_STORAGE_PATH`.
   - Uses checkpoint frequency `64`, matching the old source implementation.
   - Added backend unit tests for Captive Core env parsing, archive URL CSV cleanup, and validation errors.
   - Updated `metadata.json` and README config docs with Captive Core env mappings.

8. **Retire duplicate conversion in `stellar-live-source-datalake`.**
   - Replace duplicate conversion with `rawledger.ToRawLedger`.
   - Removes `RawLedgerChunk` vs `RawLedger` drift.

9. **Packaging polish** — done.
   - Added Nix package `.#raw-ledger-source`; `.#raw-ledger-origin` remains available and `.#default` still points to the nebu origin.
   - Added `scripts/check-packaging.sh` and `make packaging-check` to verify component version alignment across `metadata.json`, `description.yml`, `flake.nix`, and `Dockerfile.flowctl`; it also verifies `source/go.mod` requires a root module version >= `v0.2.0` because that is the first tag with exported `rawledger/`.
   - Added `docs/release.md` with the release checklist and the rule for removing the local `source/go.mod` `replace` only when publishing `source/` standalone after the required root module tag exists.
   - Updated README build docs with both Nix package targets and the packaging guard.

## Verification for completed item #1

```bash
cd /home/tillman/Documents/stellar-raw-ledger-origin/source
go test ./...
go build ./...
go vet ./...
```

## Verification for completed item #4

```bash
cd /home/tillman/Documents/stellar-raw-ledger-origin
jq empty metadata.json
cd source && go test ./... && go build ./...
cd .. && docker build -f Dockerfile.flowctl -t raw-ledger-source:flowctl-layout-smoke .
cid=$(docker create raw-ledger-source:flowctl-layout-smoke)
tmp=$(mktemp -d)
docker cp "$cid:/app/component" "$tmp/component"
docker cp "$cid:/metadata.json" "$tmp/metadata.json"
docker rm "$cid" >/dev/null
test -x "$tmp/component"
jq -r '.type, (.produces|join(",")), .env_mapping.rpc_endpoint' "$tmp/metadata.json"
```

Observed metadata check output:

```text
source
stellar.ledger.v1
RPC_ENDPOINT
```

## Verification for completed item #5

```bash
cd /home/tillman/Documents/stellar-raw-ledger-origin
RUN_TIMEOUT=45s scripts/flowctl-registry-e2e.sh
```

Observed final output:

```text
Registry smoke passed: sink received 100 event(s); cached raw-ledger-source@0.1.0 with metadata type=source produces=stellar.ledger.v1.
```

## Verification for completed item #6

```bash
cd /home/tillman/Documents/stellar-raw-ledger-origin
jq empty metadata.json
cd source
go test ./...
go build ./...
go vet ./...
```

Observed test output:

```text
ok  	github.com/withObsrvr/stellar-raw-ledger-origin/source/cmd/raw-ledger-source
ok  	github.com/withObsrvr/stellar-raw-ledger-origin/source/internal/backend
```

## Verification for completed item #7

```bash
cd /home/tillman/Documents/stellar-raw-ledger-origin
jq empty metadata.json
cd source
go test ./...
go build ./...
go vet ./...
```

Observed test output:

```text
ok  	github.com/withObsrvr/stellar-raw-ledger-origin/source/cmd/raw-ledger-source
ok  	github.com/withObsrvr/stellar-raw-ledger-origin/source/internal/backend
```

## Verification for completed item #9

```bash
cd /home/tillman/Documents/stellar-raw-ledger-origin
make packaging-check
nix build .#raw-ledger-source --no-link
nix build .#raw-ledger-origin --no-link
nix flake check
cd source
go test ./...
go build ./...
go vet ./...
```

Observed packaging check output:

```text
Packaging check passed for component version 0.1.0 (root module requirement v0.2.0)
  metadata.json:0.1.0
  description.yml:0.1.0
  flake.nix:0.1.0
  Dockerfile.flowctl:0.1.0
  source/go.mod root requirement:0.2.0
```
