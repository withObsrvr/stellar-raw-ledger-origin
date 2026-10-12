package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/xdr"
	flowctlv1 "github.com/withObsrvr/flow-proto/go/gen/flowctl/v1"
	"github.com/withObsrvr/flowctl-sdk/pkg/source"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
	"github.com/withObsrvr/stellar-raw-ledger-origin/source/ledgerstream"
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
		name               string
		params             map[string]string
		defStart, defEnd   uint32
		wantStart, wantEnd uint32
		wantErr            bool
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

func TestProducerConsumesBorrowedRawXDRBeforeAdvancing(t *testing.T) {
	ledgers := [][]byte{
		sourceFixtureLedgerXDR(t, 100, 1700000000),
		sourceFixtureLedgerXDR(t, 101, 1700000005),
	}
	factory := func(cfg ledgerstream.Config) (ledgerbackend.LedgerStream, error) {
		if cfg.Type != ledgerstream.TypeArchive {
			t.Errorf("stream config Type = %q, want %q", cfg.Type, ledgerstream.TypeArchive)
		}
		return reusedBufferStream{ledgers: ledgers}, nil
	}
	producer := newProducerWithFactory(
		ledgerstream.Config{Type: ledgerstream.TypeArchive},
		"Public Global Stellar Network ; September 2015",
		"raw-ledger-source",
		0,
		0,
		factory,
	)
	events, err := producer(context.Background(), &flowctlv1.StreamRequest{Params: map[string]string{
		"start_ledger": "100",
		"end_ledger":   "101",
	}})
	if err != nil {
		t.Fatalf("produce returned error: %v", err)
	}

	var sequences []uint32
	for event := range events.Events {
		var raw pb.RawLedger
		if err := proto.Unmarshal(event.GetPayload(), &raw); err != nil {
			t.Fatalf("decode event payload: %v", err)
		}
		var ledger xdr.LedgerCloseMeta
		if err := ledger.UnmarshalBinary(raw.GetLedgerCloseMetaXdr()); err != nil {
			t.Fatalf("decode ledger XDR: %v", err)
		}
		if raw.GetSequence() != ledger.LedgerSequence() {
			t.Fatalf("RawLedger sequence %d != XDR sequence %d", raw.GetSequence(), ledger.LedgerSequence())
		}
		sequences = append(sequences, raw.GetSequence())
	}
	if len(sequences) != 2 || sequences[0] != 100 || sequences[1] != 101 {
		t.Fatalf("sequences = %v, want [100 101]", sequences)
	}
	if err := <-events.Result; err != nil {
		t.Fatalf("terminal error: %v", err)
	}
	if !events.Bounded {
		t.Fatal("bounded range marked unbounded")
	}
}

// scriptedStream can stop after a failure or end early; the factory is never
// allowed to silently turn an incomplete bounded range into success.
type scriptedStream struct {
	items []streamItem
}
type streamItem struct {
	xdr []byte
	err error
}

func (s scriptedStream) RawLedgers(_ context.Context, _ ledgerbackend.Range, _ ...ledgerbackend.StreamOption) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		for _, item := range s.items {
			if !yield(item.xdr, item.err) {
				return
			}
		}
	}
}

func TestProducerTerminalResults(t *testing.T) {
	ledger := func(n uint32) streamItem { return streamItem{xdr: sourceFixtureLedgerXDR(t, n, 1700000000)} }
	upstream := errors.New("upstream failed")
	tests := []struct {
		name       string
		items      []streamItem
		end        string
		wantEvents int
		kind       source.ErrorKind
		cause      error
		contains   string
	}{
		{"bounded success", []streamItem{ledger(100), ledger(101)}, "101", 2, "", nil, ""},
		{"upstream before first", []streamItem{{err: upstream}}, "101", 0, source.UpstreamUnavailable, upstream, "fetch ledger"},
		{"upstream after first", []streamItem{ledger(100), {err: upstream}}, "101", 1, source.UpstreamUnavailable, upstream, "expected=101"},
		{"invalid XDR", []streamItem{{xdr: []byte{1, 2}}}, "101", 0, source.DataIntegrity, nil, "decode ledger XDR"},
		{"gap", []streamItem{ledger(100), ledger(102)}, "101", 1, source.DataIntegrity, nil, "ledger_gap"},
		{"duplicate", []streamItem{ledger(100), ledger(100)}, "101", 1, source.DataIntegrity, nil, "ledger_gap"},
		{"missing final ledger", []streamItem{ledger(100)}, "101", 1, source.DataIntegrity, nil, "incomplete bounded range"},
		{"final ledger invalid", []streamItem{ledger(100), {xdr: []byte{1}}}, "101", 1, source.DataIntegrity, nil, "decode ledger XDR"},
		{"error after final ledger", []streamItem{ledger(100), ledger(101), {err: upstream}}, "101", 2, source.UpstreamUnavailable, upstream, "fetch ledger"},
		{"extra after final ledger", []streamItem{ledger(100), ledger(101), ledger(102)}, "101", 2, source.DataIntegrity, nil, "ledger after range end"},
		{"unbounded EOF", []streamItem{ledger(100)}, "0", 1, source.UpstreamUnavailable, nil, "unexpected upstream EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := newProducerWithFactory(ledgerstream.Config{Type: ledgerstream.TypeArchive}, "testnet", "source", 0, 0,
				func(ledgerstream.Config) (ledgerbackend.LedgerStream, error) { return scriptedStream{tt.items}, nil })
			stream, err := producer(context.Background(), &flowctlv1.StreamRequest{Params: map[string]string{"start_ledger": "100", "end_ledger": tt.end}})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for range stream.Events {
				count++
			}
			if count != tt.wantEvents {
				t.Fatalf("events = %d, want %d", count, tt.wantEvents)
			}
			terminal, ok := <-stream.Result
			if !ok {
				t.Fatal("missing terminal result")
			}
			if (terminal == nil) != (tt.kind == "") {
				t.Fatalf("terminal = %v, want kind %s", terminal, tt.kind)
			}
			if tt.kind != "" {
				var classified *source.StreamError
				if !errors.As(terminal, &classified) || classified.Kind != tt.kind {
					t.Fatalf("terminal = %v, want kind %s", terminal, tt.kind)
				}
				if tt.cause != nil && !errors.Is(terminal, tt.cause) {
					t.Fatalf("lost cause: %v", terminal)
				}
				if !strings.Contains(terminal.Error(), tt.contains) {
					t.Fatalf("terminal = %v, want %s", terminal, tt.contains)
				}
			}
			select {
			case extra := <-stream.Result:
				t.Fatalf("extra terminal result: %v", extra)
			default:
			}
		})
	}
}

// The real SDK service must surface an asynchronous source failure as a gRPC
// status after delivering any events already queued by the producer.
func TestProducerErrorReachesSDK(t *testing.T) {
	cfg := source.DefaultConfig()
	cfg.FlowctlConfig.Enabled = false
	service, err := source.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	producer := newProducerWithFactory(ledgerstream.Config{Type: ledgerstream.TypeArchive}, "net", "source", 100, 101,
		func(ledgerstream.Config) (ledgerbackend.LedgerStream, error) {
			return scriptedStream{[]streamItem{{xdr: sourceFixtureLedgerXDR(t, 100, 1700000000)}, {err: errors.New("upstream failed")}}}, nil
		})
	if err := service.OnProduceStream(producer); err != nil {
		t.Fatal(err)
	}
	server := &collectingStream{ctx: context.Background()}
	err = service.StreamEvents(nil, server)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("gRPC error = %v, want Unavailable", err)
	}
	if len(server.events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(server.events))
	}
}

type collectingStream struct {
	flowctlv1.SourceService_StreamEventsServer
	ctx     context.Context
	events  []*flowctlv1.Event
	sendErr error
}

func (s *collectingStream) Context() context.Context { return s.ctx }
func (s *collectingStream) Send(ev *flowctlv1.Event) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.events = append(s.events, ev)
	return nil
}

func TestDownstreamSendFailureCancelsProducer(t *testing.T) {
	cfg := source.DefaultConfig()
	cfg.FlowctlConfig.Enabled = false
	service, err := source.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	producer := newProducerWithFactory(ledgerstream.Config{Type: ledgerstream.TypeArchive}, "net", "source", 100, 101,
		func(ledgerstream.Config) (ledgerbackend.LedgerStream, error) {
			return scriptedStream{[]streamItem{{xdr: sourceFixtureLedgerXDR(t, 100, 1700000000)}, {xdr: sourceFixtureLedgerXDR(t, 101, 1700000005)}}}, nil
		})
	if err := service.OnProduceStream(producer); err != nil {
		t.Fatal(err)
	}
	server := &collectingStream{ctx: context.Background(), sendErr: errors.New("injected downstream failure")}
	if err := service.StreamEvents(nil, server); status.Code(err) != codes.Unavailable {
		t.Fatalf("downstream status = %v, want Unavailable", err)
	}
}

func TestFinalLedgerEncodingFailure(t *testing.T) {
	items := []streamItem{
		{xdr: sourceFixtureLedgerXDR(t, 100, 1700000000)},
		{xdr: sourceFixtureLedgerXDR(t, 101, 1700000005)},
	}
	out := make(chan *flowctlv1.Event, 2)
	encodingFailure := errors.New("injected encoding failure")
	err := produceLedgersWithEncoder(context.Background(), scriptedStream{items}, ledgerbackend.BoundedRange(100, 101),
		ledgerRange{start: 100, end: 101}, "net", "source", ledgerstream.TypeArchive, out,
		func(raw *pb.RawLedger, id string) (*flowctlv1.Event, error) {
			if raw.Sequence == 101 {
				return nil, encodingFailure
			}
			return toEvent(raw, id)
		}, "injected-stream")
	var classified *source.StreamError
	if !errors.As(err, &classified) || classified.Kind != source.Internal || !errors.Is(err, encodingFailure) {
		t.Fatalf("encoding error = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("queued %d events, want 1", len(out))
	}
}

func TestTerminalLogContextDoesNotLeakUpstreamError(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	secret := "sensitive-auth-token"
	stream := scriptedStream{[]streamItem{{xdr: sourceFixtureLedgerXDR(t, 100, 1700000000)}, {err: errors.New(secret)}}}
	out := make(chan *flowctlv1.Event, 2)
	if err := produceLedgersWithEncoder(context.Background(), stream, ledgerbackend.BoundedRange(100, 101),
		ledgerRange{start: 100, end: 101}, "net", "source", ledgerstream.TypeArchive, out, toEvent, "stream-123"); err == nil {
		t.Fatal("expected upstream error")
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatal("source log leaked upstream error content")
	}
	var record map[string]interface{}
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
	}
	if record["stream_id"] != "stream-123" || record["error_kind"] != string(source.UpstreamUnavailable) || record["last_emitted_ledger"] != float64(100) || record["expected_ledger"] != float64(101) {
		t.Fatalf("terminal log fields = %v", record)
	}
}

func TestProducerStartupErrors(t *testing.T) {
	producer := newProducerWithFactory(ledgerstream.Config{Type: ledgerstream.TypeRPC}, "net", "source", 0, 0,
		func(ledgerstream.Config) (ledgerbackend.LedgerStream, error) {
			return nil, errors.New("backend unavailable")
		})
	for _, tt := range []struct {
		params map[string]string
		kind   source.ErrorKind
	}{
		{nil, source.InvalidRequest},
		{map[string]string{"start_ledger": "100"}, source.UpstreamUnavailable},
	} {
		_, err := producer(context.Background(), &flowctlv1.StreamRequest{Params: tt.params})
		var classified *source.StreamError
		if !errors.As(err, &classified) || classified.Kind != tt.kind {
			t.Fatalf("startup error = %v, want %s", err, tt.kind)
		}
	}
}

func TestProducerCancellationWithFullBuffer(t *testing.T) {
	// Deliberately never read Events. Wait until the 101st send is
	// blocked on the full buffer before canceling.
	ledgers := make([]streamItem, 102)
	for i := range ledgers {
		ledgers[i].xdr = sourceFixtureLedgerXDR(t, uint32(100+i), 1700000000)
	}
	blocked := make(chan struct{})
	producer := newProducerWithFactory(ledgerstream.Config{Type: ledgerstream.TypeRPC}, "net", "source", 100, 0,
		func(ledgerstream.Config) (ledgerbackend.LedgerStream, error) {
			return signalingStream{items: ledgers, reached: blocked}, nil
		})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := producer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("producer never filled buffer")
	}
	cancel()
	select {
	case terminal := <-stream.Result:
		if !errors.Is(terminal, context.Canceled) {
			t.Fatalf("terminal = %v", terminal)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("producer blocked after cancellation")
	}
	select {
	case _, ok := <-stream.Events:
		// Buffered events may remain; drain below to observe closure.
		_ = ok
	default:
	}
	for range stream.Events {
	}
}

type signalingStream struct {
	items   []streamItem
	reached chan struct{}
}

func (s signalingStream) RawLedgers(_ context.Context, _ ledgerbackend.Range, _ ...ledgerbackend.StreamOption) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		for i, item := range s.items {
			if i == 100 {
				close(s.reached)
			}
			if !yield(item.xdr, nil) {
				return
			}
		}
	}
}

type reusedBufferStream struct {
	ledgers [][]byte
}

func (stream reusedBufferStream) RawLedgers(context.Context, ledgerbackend.Range, ...ledgerbackend.StreamOption) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		var capacity int
		for _, ledger := range stream.ledgers {
			if len(ledger) > capacity {
				capacity = len(ledger)
			}
		}
		borrowed := make([]byte, capacity)
		for _, ledger := range stream.ledgers {
			copy(borrowed, ledger)
			if !yield(borrowed[:len(ledger)], nil) {
				return
			}
		}
	}
}

func sourceFixtureLedgerXDR(t *testing.T, sequence uint32, closeTime uint64) []byte {
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
