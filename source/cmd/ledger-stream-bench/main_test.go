package main

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type fixtureStream struct {
	ledgers [][]byte
	err     error
}

func (stream fixtureStream) RawLedgers(context.Context, ledgerbackend.Range, ...ledgerbackend.StreamOption) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		for _, ledger := range stream.ledgers {
			if !yield(ledger, nil) {
				return
			}
		}
		if stream.err != nil {
			yield(nil, stream.err)
		}
	}
}

func TestMeasure(t *testing.T) {
	stream := fixtureStream{ledgers: [][]byte{
		fixtureLedgerXDR(t, 100, 1700000000),
		fixtureLedgerXDR(t, 101, 1700000005),
	}}
	times := []time.Time{time.Unix(100, 0), time.Unix(102, 0)}
	now := func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	result, err := measure(context.Background(), stream, 100, 101, now)
	if err != nil {
		t.Fatalf("measure returned error: %v", err)
	}
	if result.Ledgers != 2 {
		t.Errorf("Ledgers = %d, want 2", result.Ledgers)
	}
	if result.Bytes == 0 {
		t.Error("Bytes = 0, want a positive count")
	}
	if result.DurationSeconds != 2 {
		t.Errorf("DurationSeconds = %f, want 2", result.DurationSeconds)
	}
	if result.LedgersPerSecond != 1 {
		t.Errorf("LedgersPerSecond = %f, want 1", result.LedgersPerSecond)
	}
}

func TestMeasureRejectsSequenceGap(t *testing.T) {
	stream := fixtureStream{ledgers: [][]byte{
		fixtureLedgerXDR(t, 100, 1700000000),
		fixtureLedgerXDR(t, 102, 1700000010),
	}}
	if _, err := measure(context.Background(), stream, 100, 101, time.Now); err == nil {
		t.Fatal("expected sequence mismatch error")
	}
}

func fixtureLedgerXDR(t *testing.T, sequence uint32, closeTime uint64) []byte {
	t.Helper()
	ledger := xdr.LedgerCloseMeta{
		V: 0,
		V0: &xdr.LedgerCloseMetaV0{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(sequence),
					ScpValue:  xdr.StellarValue{CloseTime: xdr.TimePoint(closeTime)},
				},
			},
		},
	}
	ledgerXDR, err := ledger.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal fixture ledger: %v", err)
	}
	return ledgerXDR
}
