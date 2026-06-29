// Package rawledger is the shared, adapter-neutral core that converts a
// Stellar xdr.LedgerCloseMeta into a RawLedger event.
//
// This package is the single piece reused by both runtime adapters:
//
//	rawledger                   <-- you are here (pure conversion)
//	    |
//	    +-- processors/raw-ledger-origin   (nebu CLI adapter)
//	    +-- source/cmd/raw-ledger-source   (flowctl service adapter)
//
// It deliberately knows nothing about Cobra, stdout, flowctl, gRPC,
// health endpoints, or backend selection. It depends only on the
// Stellar XDR types and the generated RawLedger message. Keep it that
// way: runtime concerns belong in the adapters.
package rawledger

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
)

// ToRawLedger converts a single LedgerCloseMeta into a RawLedger event.
//
// It marshals the ledger to its canonical binary XDR form and records
// the sequence, network passphrase, and close time. networkPassphrase
// may be empty; it is surfaced verbatim in the event's Network field so
// downstream consumers can tell mainnet ledgers from testnet ones.
//
// The only failure mode is a ledger that cannot be marshalled, which is
// returned as an error so the caller can decide whether to skip the
// ledger (origin adapters report it as a per-ledger warning and carry
// on) or abort.
func ToRawLedger(ledger xdr.LedgerCloseMeta, networkPassphrase string) (*pb.RawLedger, error) {
	xdrBytes, err := ledger.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal ledger %d: %w", ledger.LedgerSequence(), err)
	}

	return &pb.RawLedger{
		Sequence:           ledger.LedgerSequence(),
		LedgerCloseMetaXdr: xdrBytes,
		Network:            networkPassphrase,
		ClosedAt:           ledger.LedgerCloseTime(),
	}, nil
}
