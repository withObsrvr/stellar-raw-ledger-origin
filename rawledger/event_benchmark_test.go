package rawledger

import (
	"testing"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
)

var benchmarkRawLedger *pb.RawLedger

func BenchmarkToRawLedger(b *testing.B) {
	ledger := fixtureLedger(60200000, 1700000000)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		benchmarkRawLedger, err = ToRawLedger(ledger, "Public Global Stellar Network ; September 2015")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFromXDR(b *testing.B) {
	ledgerXDR, err := fixtureLedger(60200000, 1700000000).MarshalBinary()
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(ledgerXDR)))
	b.ReportAllocs()
	for b.Loop() {
		benchmarkRawLedger, err = FromXDR(ledgerXDR, "Public Global Stellar Network ; September 2015")
		if err != nil {
			b.Fatal(err)
		}
	}
}
