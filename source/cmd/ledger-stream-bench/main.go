// Command ledger-stream-bench measures raw acquisition independently of
// extraction, protobuf expansion, and persistence.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/withObsrvr/stellar-raw-ledger-origin/source/ledgerstream"
)

type benchmarkResult struct {
	Backend           ledgerstream.Type `json:"backend"`
	StartLedger       uint32            `json:"start_ledger"`
	EndLedger         uint32            `json:"end_ledger"`
	Ledgers           uint64            `json:"ledgers"`
	Bytes             uint64            `json:"bytes"`
	DurationSeconds   float64           `json:"duration_seconds"`
	LedgersPerSecond  float64           `json:"ledgers_per_second"`
	BytesPerSecond    float64           `json:"bytes_per_second"`
	BufferSize        uint32            `json:"buffer_size,omitempty"`
	NumWorkers        uint32            `json:"num_workers,omitempty"`
	LedgersPerFile    uint32            `json:"ledgers_per_file,omitempty"`
	FilesPerPartition uint32            `json:"files_per_partition,omitempty"`
}

func main() {
	startDefault := envUint32("START_LEDGER", 0)
	endDefault := envUint32("END_LEDGER", 0)
	start := flag.Uint64("start-ledger", uint64(startDefault), "first ledger to fetch (inclusive)")
	end := flag.Uint64("end-ledger", uint64(endDefault), "last ledger to fetch (inclusive)")
	flag.Parse()

	if *start == 0 || *end == 0 || *start > math.MaxUint32 || *end > math.MaxUint32 || *end < *start {
		log.Fatal("a valid bounded --start-ledger/--end-ledger range is required")
	}

	streamConfig := ledgerstream.ConfigFromEnv()
	stream, err := ledgerstream.New(streamConfig)
	if err != nil {
		log.Fatalf("create ledger stream: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	result, err := measure(ctx, stream, uint32(*start), uint32(*end), time.Now)
	if err != nil {
		log.Fatalf("measure ledger stream: %v", err)
	}
	result.Backend = streamConfig.Type
	result.BufferSize = streamConfig.BufferSize
	result.NumWorkers = streamConfig.NumWorkers
	result.LedgersPerFile = streamConfig.LedgersPerFile
	result.FilesPerPartition = streamConfig.FilesPerPartition

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		log.Fatalf("encode result: %v", err)
	}
}

func measure(
	ctx context.Context,
	stream ledgerbackend.LedgerStream,
	start uint32,
	end uint32,
	now func() time.Time,
) (benchmarkResult, error) {
	result := benchmarkResult{StartLedger: start, EndLedger: end}
	if start == 0 || end < start {
		return result, fmt.Errorf("invalid range [%d,%d]", start, end)
	}

	expectedSequence := start
	started := now()
	for ledgerXDR, streamErr := range stream.RawLedgers(ctx, ledgerbackend.BoundedRange(start, end)) {
		if streamErr != nil {
			return result, streamErr
		}
		sequence, err := xdr.LedgerCloseMetaView(ledgerXDR).LedgerSequence()
		if err != nil {
			return result, fmt.Errorf("read ledger %d sequence: %w", expectedSequence, err)
		}
		if sequence != expectedSequence {
			return result, fmt.Errorf("stream sequence mismatch: got %d, want %d", sequence, expectedSequence)
		}
		result.Ledgers++
		result.Bytes += uint64(len(ledgerXDR))
		if expectedSequence != end {
			expectedSequence++
		}
	}

	wantLedgers := uint64(end) - uint64(start) + 1
	if result.Ledgers != wantLedgers {
		return result, fmt.Errorf("stream ended after %d ledgers, want %d", result.Ledgers, wantLedgers)
	}
	result.DurationSeconds = now().Sub(started).Seconds()
	if result.DurationSeconds <= 0 {
		return result, fmt.Errorf("non-positive measurement duration %f", result.DurationSeconds)
	}
	result.LedgersPerSecond = float64(result.Ledgers) / result.DurationSeconds
	result.BytesPerSecond = float64(result.Bytes) / result.DurationSeconds
	return result, nil
}

func envUint32(key string, fallback uint32) uint32 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	number, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fallback
	}
	return uint32(number)
}
