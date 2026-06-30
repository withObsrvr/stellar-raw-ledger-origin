# Release / packaging notes

Current component version: `0.1.0`.

Component version-bearing files that must move together:

- `description.yml` (`processor.version`)
- `metadata.json` (`version`)
- `Dockerfile.flowctl` (`org.opencontainers.image.version`)
- `flake.nix` (`version`)

`source/go.mod` has a separate root-module requirement. It must point at a
root tag that contains the exported `rawledger/` and `gen/` packages (minimum
`github.com/withObsrvr/stellar-raw-ledger-origin v0.2.0`), but it does not
need to match the component version.

Run the packaging guard before tagging:

```bash
make packaging-check
nix build .#raw-ledger-origin
nix build .#raw-ledger-source
```

The source module currently keeps:

```go
replace github.com/withObsrvr/stellar-raw-ledger-origin => ../
```

That is intentional for this monorepo checkout and for the Nix source package,
because the flowctl source consumes the local shared `rawledger` core. If the
`source/` module is published as a standalone module outside this checkout,
remove that `replace` only after the root module tag named by `source/go.mod`
exists and contains the required `rawledger` / `gen` packages.
