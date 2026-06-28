package rawledger

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
)

// TestRawLedgerWireContract pins the wire contract that makes this repo's
// RawLedger interchangeable with flow-proto's stellar.v1.RawLedger.
//
// Because we deliberately own a lean, messages-only copy of the proto
// (instead of importing flow-proto and dragging in gRPC), nothing at
// compile time guarantees the two stay aligned. This guard does: if a
// field number, name, kind, or the proto package/message name ever
// drifts from flow-proto, this test fails and tells you to re-sync.
//
// Reference (github.com/withObsrvr/flow-proto, proto/stellar/v1/raw_ledger.proto):
//
//	package stellar.v1;
//	message RawLedger {
//	  uint32 sequence = 1;
//	  bytes  ledger_close_meta_xdr = 2;
//	  string network = 3;
//	  int64  closed_at = 4;
//	}
func TestRawLedgerWireContract(t *testing.T) {
	md := (&pb.RawLedger{}).ProtoReflect().Descriptor()

	if got := string(md.ParentFile().Package()); got != "stellar.v1" {
		t.Errorf("proto package = %q, want %q (must match flow-proto)", got, "stellar.v1")
	}
	if got := string(md.Name()); got != "RawLedger" {
		t.Errorf("message name = %q, want %q (must match flow-proto)", got, "RawLedger")
	}

	type field struct {
		num  int32
		name string
		kind protoreflect.Kind
	}
	want := []field{
		{1, "sequence", protoreflect.Uint32Kind},
		{2, "ledger_close_meta_xdr", protoreflect.BytesKind},
		{3, "network", protoreflect.StringKind},
		{4, "closed_at", protoreflect.Int64Kind},
	}

	fields := md.Fields()
	if fields.Len() != len(want) {
		t.Fatalf("field count = %d, want %d (flow-proto drift?)", fields.Len(), len(want))
	}
	for _, w := range want {
		f := fields.ByNumber(protoreflect.FieldNumber(w.num))
		if f == nil {
			t.Errorf("missing field number %d (%s)", w.num, w.name)
			continue
		}
		if string(f.Name()) != w.name {
			t.Errorf("field %d name = %q, want %q", w.num, f.Name(), w.name)
		}
		if f.Kind() != w.kind {
			t.Errorf("field %d (%s) kind = %s, want %s", w.num, w.name, f.Kind(), w.kind)
		}
	}
}
