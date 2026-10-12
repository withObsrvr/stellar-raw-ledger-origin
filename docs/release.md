# Release and packaging

## Published release

`v0.2.4` and `source/v0.2.4` point at `9e35130`. The Linux/amd64 image
`docker.io/withobsrvr/raw-ledger-source:0.2.4` is published at
`docker.io/withobsrvr/raw-ledger-source@sha256:16828cdbd3c612b0c0528862375c690407f516c6f640b6bd737301b6834f67f0`.
It was not signed. Deploy by digest, not `latest`. Do not treat a locally built
image for the next version as an update to that immutable release.

## Next release (prepared, not published)

Component version `0.2.5` adds source-side progress telemetry and upgrades
nebu to `v0.6.15`. Both Go modules use Stellar SDK `v0.7.3`; source uses
flowctl-sdk `v0.1.4`. Source pins the existing root `v0.2.3` tag for the
shared `rawledger/` and `gen/` packages and does not need a local replace.
Base images are pinned by index digest in `Dockerfile.flowctl`. Refresh these
explicitly with security review, tests, and a *new version*; never change an
existing release tag in place.

Keep `description.yml` (`processor.version` and `repo.ref`), `metadata.json`,
`Dockerfile.flowctl`, `flake.nix`, and the binary version in sync. Run:

```bash
go test ./... && go vet ./...
(cd source && go test ./... && go vet ./... && go build ./...)
make packaging-check
nix build .#raw-ledger-origin --no-link
nix build .#raw-ledger-source --no-link
```

After merge, configure repository secrets `DOCKERHUB_USERNAME` and
`DOCKERHUB_TOKEN` for `.github/workflows/publish-source.yml`. Create the root
tag `v0.2.5` and nested Go-module tag `source/v0.2.5` on the same commit (if
unused). The root tag triggers the workflow to publish an amd64 image under
`docker.io/withobsrvr/raw-ledger-source:0.2.5`, produce an SPDX SBOM, and
keylessly sign/attest the digest. Verify the workflow, record the **remote**
manifest digest, and test the image by digest before deployment. Neither the `0.2.5`
tag nor its image or signature exists yet. Do not modify or push `0.2.4` again.

Roll back to the previously deployed image digest; consumers resume from their
own durable checkpoint. The source does not own a checkpoint and does not
replay automatically. The current flowctl supervisor does not yet mark a
pipeline failed on source stream errors; deploy the separate flowctl fix before
relying on pipeline-level failure status. No flow-proto change is needed.
