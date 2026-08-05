package main

import (
	"context"
	"fmt"
	"log"
	"strconv"

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

// newProducer returns the flowctl producer. Each request consumes its range
// through a LedgerStream, converts the borrowed XDR with zero-copy views, and
// marshals it into a self-contained flowctl Event before advancing the stream.
func newProducer(streamCfg ledgerstream.Config, networkPassphrase, componentID string, defStart, defEnd uint32) source.ProducerFunc {
	return newProducerWithFactory(streamCfg, networkPassphrase, componentID, defStart, defEnd, ledgerstream.New)
}

type ledgerStreamFactory func(ledgerstream.Config) (ledgerbackend.LedgerStream, error)

func newProducerWithFactory(
	streamCfg ledgerstream.Config,
	networkPassphrase string,
	componentID string,
	defStart uint32,
	defEnd uint32,
	newStream ledgerStreamFactory,
) source.ProducerFunc {
	return func(ctx context.Context, req *flowctlv1.StreamRequest) (<-chan *flowctlv1.Event, error) {
		var params map[string]string
		if req != nil {
			params = req.GetParams()
		}
		lr, err := rangeFromParams(params, defStart, defEnd)
		if err != nil {
			return nil, err
		}

		stream, err := newStream(streamCfg)
		if err != nil {
			return nil, err
		}

		var requestedRange ledgerbackend.Range
		if lr.end == 0 {
			requestedRange = ledgerbackend.UnboundedRange(lr.start)
		} else {
			requestedRange = ledgerbackend.BoundedRange(lr.start, lr.end)
		}

		out := make(chan *flowctlv1.Event, 100)
		go func() {
			defer close(out)

			expectedSequence := lr.start
			for ledgerXDR, streamErr := range stream.RawLedgers(ctx, requestedRange) {
				if streamErr != nil {
					if ctx.Err() != nil {
						return // consumer disconnected / shutdown
					}
					log.Printf("stream ledger %d: %v", expectedSequence, streamErr)
					return
				}

				raw, err := rawledger.FromXDR(ledgerXDR, networkPassphrase)
				if err != nil {
					log.Printf("convert ledger %d: %v", expectedSequence, err)
					return
				}
				if raw.GetSequence() != expectedSequence {
					log.Printf("stream sequence mismatch: got %d, want %d", raw.GetSequence(), expectedSequence)
					return
				}
				ev, err := toEvent(raw, componentID)
				if err != nil {
					log.Printf("encode ledger %d: %v", expectedSequence, err)
					return
				}

				select {
				case <-ctx.Done():
					return
				case out <- ev:
				}
				expectedSequence++
			}
		}()

		return out, nil
	}
}
