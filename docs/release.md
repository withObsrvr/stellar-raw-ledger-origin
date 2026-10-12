# Release / packaging notes

Prepared component version: `0.2.4` (not published until the release commit,
tag, and image push). The repository's existing root `v0.2.3` tag contains
the exported `rawledger/` and `gen/` packages; `source/go.mod` pins it without
a relative replacement, so source builds do not require a parent checkout.
Root and source Go modules both use Stellar SDK `v0.7.3`; the source uses
flowctl-sdk `v0.1.4`.

Keep `description.yml` (`processor.version` and `repo.ref`), `metadata.json`,
`Dockerfile.flowctl`, `flake.nix`, and the binary version in sync. Before
publishing, run:

```bash
go test ./... && go vet ./...
(cd source && go test ./... && go vet ./... && go build ./...)
make packaging-check
nix build .#raw-ledger-origin
nix build .#raw-ledger-source
```

After review and merge, tag the resulting root commit `v0.2.4` and the nested
Go module `source/v0.2.4` (after checking both tag names are unused). Build and
push `docker.io/withobsrvr/raw-ledger-source:0.2.4`, record its immutable digest,
then deploy by digest, not `latest`. Do not claim the new component released
until the tag and image exist. Roll back to the previously deployed digest;
consumers resume from their own durable checkpoint. The source does not own a
checkpoint and does not replay automatically.

`raw-ledger-source` requires flowctl-sdk >= `v0.1.4` for error-aware streams.
The current flowctl supervisor does not yet mark a pipeline failed on source
stream errors; deploy the corresponding flowctl change before relying on
pipeline-level failure status. No flow-proto change is needed for this release.
