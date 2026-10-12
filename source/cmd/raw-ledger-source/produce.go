package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	flowctlv1 "github.com/withObsrvr/flow-proto/go/gen/flowctl/v1"
	"github.com/withObsrvr/flowctl-sdk/pkg/source"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
	"github.com/withObsrvr/stellar-raw-ledger-origin/rawledger"
	"github.com/withObsrvr/stellar-raw-ledger-origin/source/ledgerstream"
)

// rawLedgerEventType is the flowctl routing type for raw Stellar ledgers.
// Existing flowctl Stellar processors consume this event type and decode
// the payload as flow-proto's stellar.v1.RawLedger protobuf message.
const rawLedgerEventType = "stellar.ledger.v1"

// ledgerRange is the parsed [start, end] window. end == 0 means unbounded.
type ledgerRange struct {
	start uint32
	end   uint32
}

// rangeFromParams reads start_ledger / end_ledger from a flowctl
// StreamRequest, falling back to the supplied defaults (typically from
// the environment) when a param is absent. end == 0 is unbounded.
func rangeFromParams(params map[string]string, defStart, defEnd uint32) (ledgerRange, error) {
	r := ledgerRange{start: defStart, end: defEnd}
	if v, ok := params["start_ledger"]; ok && v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return r, fmt.Errorf("invalid start_ledger %q: %w", v, err)
		}
		r.start = uint32(n)
	}
	if v, ok := params["end_ledger"]; ok && v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return r, fmt.Errorf("invalid end_ledger %q: %w", v, err)
		}
		r.end = uint32(n)
	}
	if r.start == 0 {
		return r, fmt.Errorf("start_ledger is required (set START_LEDGER or pass start_ledger param)")
	}
	if r.end != 0 && r.end < r.start {
		return r, fmt.Errorf("end_ledger (%d) must be >= start_ledger (%d)", r.end, r.start)
	}
	return r, nil
}

// toEvent wraps a RawLedger in the flowctl Event envelope. The RawLedger
// is carried as marshalled protobuf bytes in the payload; the proto name
// is advertised via Type and the format via ContentType.
func toEvent(raw *pb.RawLedger, componentID string) (*flowctlv1.Event, error) {
	payload, err := proto.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal RawLedger %d: %w", raw.GetSequence(), err)
	}
	return &flowctlv1.Event{
		Id:                fmt.Sprintf("raw-ledger-%d", raw.GetSequence()),
		Type:              rawLedgerEventType,
		Payload:           payload,
		ContentType:       "application/protobuf",
		Timestamp:         timestamppb.Now(),
		SourceComponentId: componentID,
		Metadata:          map[string]string{"network": raw.GetNetwork()},
		StellarCursor:     &flowctlv1.StellarCursor{LedgerSequence: uint64(raw.GetSequence())},
	}, nil
}

// newProducer returns the error-aware flowctl producer. Each request consumes its range
// through a LedgerStream, converts the borrowed XDR with zero-copy views, and
// marshals it into a self-contained flowctl Event before advancing the stream.
func newProducer(streamCfg ledgerstream.Config, networkPassphrase, componentID string, defStart, defEnd uint32) source.ProducerStreamFunc {
	return newProducerWithFactory(streamCfg, networkPassphrase, componentID, defStart, defEnd, ledgerstream.New)
}

// A successful send means queued for the SDK, not yet delivered to the client;
// the SDK reports downstream Send failures separately.
// eventEncoder is injectable to exercise failures at the final ledger; ordinary
// protobuf messages rarely fail to marshal without a deliberately bad value.
type eventEncoder func(*pb.RawLedger, string) (*flowctlv1.Event, error)

func produceLedgersWithEncoder(ctx context.Context, stream ledgerbackend.LedgerStream, requestedRange ledgerbackend.Range, lr ledgerRange, network, componentID string, backend ledgerstream.Type, out chan<- *flowctlv1.Event, encode eventEncoder, streamID string) error {
	expected := lr.start
	lastEmitted := uint32(0)
	log := slog.With("component", componentID, "stream_id", streamID, "backend", backend,
		"requested_start_ledger", lr.start, "requested_end_ledger", lr.end)
	log.Info("stream started", "expected_ledger", expected)
	failure := func(kind source.ErrorKind, operation string, cause error) error {
		log.Error("stream failed", "expected_ledger", expected, "last_emitted_ledger", lastEmitted,
			"error_kind", kind, "operation", operation)
		return &source.StreamError{Kind: kind, Cause: fmt.Errorf("stream_id=%s backend=%s range=[%d,%d] expected=%d last_emitted=%d %s: %w", streamID, backend, lr.start, lr.end, expected, lastEmitted, operation, cause)}
	}
	for ledgerXDR, streamErr := range stream.RawLedgers(ctx, requestedRange) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if streamErr != nil {
			return failure(source.UpstreamUnavailable, "fetch ledger", streamErr)
		}
		if (lr.end != 0 && lastEmitted == lr.end) || lastEmitted == ^uint32(0) {
			return failure(source.DataIntegrity, "ledger after range end", fmt.Errorf("unexpected extra ledger"))
		}
		raw, err := rawledger.FromXDR(ledgerXDR, network)
		if err != nil {
			return failure(source.DataIntegrity, "decode ledger XDR", err)
		}
		if raw.GetSequence() != expected {
			return failure(source.DataIntegrity, "ledger_gap", fmt.Errorf("got sequence %d, want %d", raw.GetSequence(), expected))
		}
		ev, err := encode(raw, componentID)
		if err != nil {
			return failure(source.Internal, "encode ledger event", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- ev:
		}
		lastEmitted = expected
		if expected != ^uint32(0) {
			expected++
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if lr.end != 0 {
		if lastEmitted == lr.end {
			log.Info("stream completed", "last_emitted_ledger", lastEmitted)
			return nil
		}
		return failure(source.DataIntegrity, "incomplete bounded range", fmt.Errorf("ended before ledger %d", lr.end))
	}
	return failure(source.UpstreamUnavailable, "unbounded stream ended", fmt.Errorf("unexpected upstream EOF"))
}

type ledgerStreamFactory func(ledgerstream.Config) (ledgerbackend.LedgerStream, error)

func newProducerWithFactory(
	streamCfg ledgerstream.Config,
	networkPassphrase string,
	componentID string,
	defStart uint32,
	defEnd uint32,
	newStream ledgerStreamFactory,
) source.ProducerStreamFunc {
	return func(ctx context.Context, req *flowctlv1.StreamRequest) (*source.ProducerStream, error) {
		var params map[string]string
		if req != nil {
			params = req.GetParams()
		}
		lr, err := rangeFromParams(params, defStart, defEnd)
		if err != nil {
			return nil, &source.StreamError{Kind: source.InvalidRequest, Cause: err}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		stream, err := newStream(streamCfg)
		if err != nil {
			return nil, &source.StreamError{Kind: source.UpstreamUnavailable, Cause: fmt.Errorf("initialize %s backend: %w", streamCfg.Type, err)}
		}

		var requestedRange ledgerbackend.Range
		if lr.end == 0 {
			requestedRange = ledgerbackend.UnboundedRange(lr.start)
		} else {
			requestedRange = ledgerbackend.BoundedRange(lr.start, lr.end)
		}

		out := make(chan *flowctlv1.Event, 100)
		result := make(chan error, 1)
		streamID := uuid.NewString()
		go func() {
			// The SDK drains buffered events before reading Result. Publishing the
			// single terminal result before closing Events makes EOF unambiguous.
			result <- produceLedgersWithEncoder(ctx, stream, requestedRange, lr, networkPassphrase, componentID, streamCfg.Type, out, toEvent, streamID)
			close(out)
		}()

		return &source.ProducerStream{Events: out, Result: result, Bounded: lr.end != 0}, nil
	}
}
