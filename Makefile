# Shared raw-ledger core plus nebu-style origin and flowctl source binaries.
#
# Run inside the nix dev shell so protoc / protoc-gen-go are present:
#   nix develop --command make <target>

MODULE := github.com/withObsrvr/stellar-raw-ledger-origin
PROTO  := proto/stellar/v1/raw_ledger.proto

.PHONY: all proto build test tidy source-build source-test source-vet source-tidy nix-build nix-build-source packaging-check describe clean

all: proto build test

# Regenerate gen/ from proto/. Messages-only (no gRPC) by design.
proto:
	protoc -I proto \
		--go_out=. --go_opt=module=$(MODULE) \
		$(PROTO)

tidy:
	go mod tidy

source-tidy:
	cd source && go mod tidy

build:
	go build ./...

test:
	go test ./...

source-build:
	cd source && go build ./...

source-test:
	cd source && go test ./...

source-vet:
	cd source && go vet ./...

nix-build:
	nix build .#raw-ledger-origin

nix-build-source:
	nix build .#raw-ledger-source

packaging-check:
	scripts/check-packaging.sh

# Smoke-test the machine-readable describe envelope.
describe: build
	go run ./processors/raw-ledger-origin/cmd/raw-ledger-origin --describe-json | jq .

clean:
	rm -rf gen
