package main

import (
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
)

func TestComponentIDFromEnv(t *testing.T) {
	t.Run("flowctl component id wins", func(t *testing.T) {
		t.Setenv("FLOWCTL_COMPONENT_ID", "stellar-source")
		t.Setenv("SOURCE_ID", "raw-ledger-source")
		if got := componentIDFromEnv(); got != "stellar-source" {
			t.Fatalf("componentIDFromEnv() = %q, want %q", got, "stellar-source")
		}
	})

	t.Run("source id fallback", func(t *testing.T) {
		t.Setenv("SOURCE_ID", "local-source")
		if got := componentIDFromEnv(); got != "local-source" {
			t.Fatalf("componentIDFromEnv() = %q, want %q", got, "local-source")
		}
	})

	t.Run("default fallback", func(t *testing.T) {
		if got := componentIDFromEnv(); got != "raw-ledger-source" {
			t.Fatalf("componentIDFromEnv() = %q, want %q", got, "raw-ledger-source")
		}
	})
}

func TestRangeFromParams(t *testing.T) {
	tests := []struct {
		name              string
		params            map[string]string
		defStart, defEnd  uint32
		wantStart, wantEnd uint32
		wantErr           bool
	}{
		{"params override defaults", map[string]string{"start_ledger": "100", "end_ledger": "200"}, 1, 2, 100, 200, false},
		{"fall back to defaults", nil, 50, 0, 50, 0, false},
		{"unbounded when end absent", map[string]string{"start_ledger": "10"}, 0, 0, 10, 0, false},
		{"missing start is an error", nil, 0, 0, 0, 0, true},
		{"end before start is an error", map[string]string{"start_ledger": "200", "end_ledger": "100"}, 0, 0, 0, 0, true},
		{"non-numeric start is an error", map[string]string{"start_ledger": "abc"}, 0, 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rangeFromParams(tt.params, tt.defStart, tt.defEnd)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got range %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.start != tt.wantStart || got.end != tt.wantEnd {
				t.Errorf("got [%d,%d], want [%d,%d]", got.start, got.end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// TestToEvent verifies the flowctl envelope wraps a decodable RawLedger
// payload and advertises the flowctl routing type.
func TestToEvent(t *testing.T) {
	raw := &pb.RawLedger{
		Sequence:           60200000,
		LedgerCloseMetaXdr: []byte{0x01, 0x02, 0x03},
		Network:            "Public Global Stellar Network ; September 2015",
		ClosedAt:           1700000000,
	}

	ev, err := toEvent(raw, "raw-ledger-source")
	if err != nil {
		t.Fatalf("toEvent error: %v", err)
	}
	if ev.GetType() != rawLedgerEventType {
		t.Errorf("Type = %q, want %q", ev.GetType(), rawLedgerEventType)
	}
	if ev.GetContentType() != "application/protobuf" {
		t.Errorf("ContentType = %q", ev.GetContentType())
	}
	if got := ev.GetStellarCursor().GetLedgerSequence(); got != uint64(raw.Sequence) {
		t.Errorf("cursor LedgerSequence = %d, want %d", got, raw.Sequence)
	}

	// Payload must round-trip back to an identical RawLedger.
	var decoded pb.RawLedger
	if err := proto.Unmarshal(ev.GetPayload(), &decoded); err != nil {
		t.Fatalf("payload does not decode as RawLedger: %v", err)
	}
	if !proto.Equal(raw, &decoded) {
		t.Errorf("decoded payload != original\n got: %v\nwant: %v", &decoded, raw)
	}
}
