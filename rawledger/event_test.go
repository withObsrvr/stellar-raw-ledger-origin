package rawledger

import (
	"bytes"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// fixtureLedger builds a minimal but valid V0 LedgerCloseMeta carrying a
// known sequence and close time.
func fixtureLedger(seq uint32, closeTime uint64) xdr.LedgerCloseMeta {
	return xdr.LedgerCloseMeta{
		V: 0,
		V0: &xdr.LedgerCloseMetaV0{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(seq),
					ScpValue:  xdr.StellarValue{CloseTime: xdr.TimePoint(closeTime)},
				},
			},
		},
	}
}

func TestToRawLedger_PopulatesFields(t *testing.T) {
	const (
		seq        = uint32(60200000)
		closeTime  = uint64(1700000000)
		passphrase = "Test SDF Network ; September 2015"
	)
	ledger := fixtureLedger(seq, closeTime)

	ev, err := ToRawLedger(ledger, passphrase)
	if err != nil {
		t.Fatalf("ToRawLedger returned error: %v", err)
	}

	if ev.GetSequence() != seq {
		t.Errorf("Sequence = %d, want %d", ev.GetSequence(), seq)
	}
	if ev.GetClosedAt() != int64(closeTime) {
		t.Errorf("ClosedAt = %d, want %d", ev.GetClosedAt(), closeTime)
	}
	if ev.GetNetwork() != passphrase {
		t.Errorf("Network = %q, want %q", ev.GetNetwork(), passphrase)
	}
	if len(ev.GetLedgerCloseMetaXdr()) == 0 {
		t.Fatal("LedgerCloseMetaXdr is empty")
	}
}

// TestToRawLedger_XDRRoundTrips proves the embedded bytes are the
// canonical XDR encoding: unmarshalling them back yields an identical
// ledger. This is the property both adapters and every downstream
// consumer rely on.
func TestToRawLedger_XDRRoundTrips(t *testing.T) {
	ledger := fixtureLedger(12345, 1699999999)

	ev, err := ToRawLedger(ledger, "")
	if err != nil {
		t.Fatalf("ToRawLedger returned error: %v", err)
	}

	var got xdr.LedgerCloseMeta
	if err := got.UnmarshalBinary(ev.GetLedgerCloseMetaXdr()); err != nil {
		t.Fatalf("UnmarshalBinary of embedded XDR failed: %v", err)
	}
	if got.LedgerSequence() != ledger.LedgerSequence() {
		t.Errorf("round-tripped sequence = %d, want %d", got.LedgerSequence(), ledger.LedgerSequence())
	}
	if got.LedgerCloseTime() != ledger.LedgerCloseTime() {
		t.Errorf("round-tripped close time = %d, want %d", got.LedgerCloseTime(), ledger.LedgerCloseTime())
	}
}

func TestToRawLedger_EmptyPassphraseAllowed(t *testing.T) {
	ev, err := ToRawLedger(fixtureLedger(1, 1), "")
	if err != nil {
		t.Fatalf("ToRawLedger returned error: %v", err)
	}
	if ev.GetNetwork() != "" {
		t.Errorf("Network = %q, want empty", ev.GetNetwork())
	}
}

func TestFromXDR_PopulatesFieldsWithoutChangingBytes(t *testing.T) {
	const (
		sequence   = uint32(60200000)
		closeTime  = uint64(1700000000)
		passphrase = "Public Global Stellar Network ; September 2015"
	)
	ledgerXDR, err := fixtureLedger(sequence, closeTime).MarshalBinary()
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	event, err := FromXDR(ledgerXDR, passphrase)
	if err != nil {
		t.Fatalf("FromXDR returned error: %v", err)
	}
	if event.GetSequence() != sequence {
		t.Errorf("Sequence = %d, want %d", event.GetSequence(), sequence)
	}
	if event.GetClosedAt() != int64(closeTime) {
		t.Errorf("ClosedAt = %d, want %d", event.GetClosedAt(), closeTime)
	}
	if event.GetNetwork() != passphrase {
		t.Errorf("Network = %q, want %q", event.GetNetwork(), passphrase)
	}
	if !bytes.Equal(event.GetLedgerCloseMetaXdr(), ledgerXDR) {
		t.Fatal("LedgerCloseMetaXdr differs from the source bytes")
	}
}

func TestFromXDR_RejectsMalformedLedger(t *testing.T) {
	if _, err := FromXDR([]byte{0, 0, 0}, ""); err == nil {
		t.Fatal("expected malformed XDR error")
	}
}
