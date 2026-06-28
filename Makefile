# Milestone 1: nebu-style raw-ledger-origin binary.
#
# Run inside the nix dev shell so protoc / protoc-gen-go are present:
#   nix develop --command make <target>

MODULE := github.com/withObsrvr/stellar-raw-ledger-origin
PROTO  := proto/stellar/v1/raw_ledger.proto

.PHONY: all proto build test tidy describe clean

all: proto build test

# Regenerate gen/ from proto/. Messages-only (no gRPC) by design.
proto:
	protoc -I proto \
		--go_out=. --go_opt=module=$(MODULE) \
		$(PROTO)

tidy:
	go mod tidy

build:
	go build ./...

test:
	go test ./...

# Smoke-test the machine-readable describe envelope.
describe: build
	go run ./processors/raw-ledger-origin/cmd/raw-ledger-origin --describe-json | jq .

clean:
	rm -rf gen
