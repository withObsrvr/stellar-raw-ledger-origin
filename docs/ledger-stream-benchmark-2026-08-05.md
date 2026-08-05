# Ledger stream acquisition baseline — 2026-08-05

This measurement isolates raw ledger acquisition from extraction, protobuf
expansion, and persistence. It is a source-capacity baseline, not an end-to-end
backfill result.

## Mainnet archive result

Environment:

- source: AWS public Stellar archive in `us-east-2`
- range: ledgers 62,080,000 through 62,080,999
- archive layout: one ledger per object, 64,000 objects per path partition
- read-ahead buffer: 1,000 ledgers
- fetch workers: 50
- client: local AMD Ryzen 9 7940HS workstation, outside the archive region

Observed result:

```json
{
  "backend": "ARCHIVE",
  "start_ledger": 62080000,
  "end_ledger": 62080999,
  "ledgers": 1000,
  "bytes": 1733356100,
  "duration_seconds": 25.002705034,
  "ledgers_per_second": 39.995672413850706,
  "bytes_per_second": 69326742.75214984,
  "buffer_size": 1000,
  "num_workers": 50,
  "ledgers_per_file": 1,
  "files_per_partition": 64000
}
```

Command:

```bash
cd source
BACKEND_TYPE=ARCHIVE \
ARCHIVE_STORAGE_TYPE=S3 \
ARCHIVE_BUCKET_NAME=aws-public-blockchain \
ARCHIVE_PATH=v1.1/stellar/ledgers/pubnet \
AWS_REGION=us-east-2 \
LEDGERS_PER_FILE=1 \
FILES_PER_PARTITION=64000 \
NETWORK_PASSPHRASE='Public Global Stellar Network ; September 2015' \
BUFFER_SIZE=1000 \
NUM_WORKERS=50 \
go run ./cmd/ledger-stream-bench \
  --start-ledger 62080000 \
  --end-ledger 62080999
```

## Interpretation

The measured ledger rate was limited by approximately 69.3 MB/s of raw input,
not by sequence parsing. This recent, high-activity range averaged 1.73 MB of
raw XDR per ledger. At the same ledger shape, 1,000 ledgers per second requires
about 1.73 GB/s of aggregate archive bandwidth before extraction begins.

This supports the distributed design: run disjoint range workers near the
archive, then measure aggregate throughput. It does not support projecting
full-history time from this one recent range because older ledgers are much
smaller.

## RawLedger conversion microbenchmark

On the same workstation, using the repository's minimal valid ledger fixture:

| Conversion | Time | Allocated bytes | Allocations |
| --- | ---: | ---: | ---: |
| parsed `ToRawLedger` | 500–513 ns/op | 1,128 B/op | 7 allocs/op |
| validated view-backed `FromXDR` | 100–102 ns/op | 96 B/op | 1 alloc/op |

The microbenchmark demonstrates the effect of removing the full object
re-marshal from the source adapter. It is not a replacement for the real
archive benchmark above.

## Next acquisition gate

Run the same range from compute in or near `us-east-2`, sweep fetch workers and
buffer sizes, and then run disjoint ranges across multiple processes. Record
ledger rate, byte rate, object request rate, retries, and cost before selecting
fleet defaults.
