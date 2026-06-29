// Command raw-ledger-origin is the nebu-style adapter for the shared
// raw-ledger core. It emits each Stellar ledger as a RawLedger JSONL
// event on stdout.
//
// All runtime behaviour (flags, stdin/file/RPC input modes,
// --describe-json, JSONL marshalling, graceful shutdown) comes from
// nebu's cli.RunProtoOriginCLI helper. This file owns the nebu coupling
// so the core in the rawledger package stays adapter-neutral.
//
// Examples:
//
//	raw-ledger-origin --start-ledger 60200000 --end-ledger 60200010
//	nebu fetch 60200000 60200010 | raw-ledger-origin
//	raw-ledger-origin --describe-json
package main

import (
	"context"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/withObsrvr/nebu/pkg/processor"
	"github.com/withObsrvr/nebu/pkg/processor/cli"

	pb "github.com/withObsrvr/stellar-raw-ledger-origin/gen/stellar/v1"
	"github.com/withObsrvr/stellar-raw-ledger-origin/rawledger"
)

// origin adapts the pure rawledger core to nebu's ProtoOriginProcessor
// interface. It is a thin shell: ProcessLedger delegates straight to
// rawledger.ToRawLedger and pushes the result onto the emitter.
type origin struct {
	networkPassphrase string
	emitter           *processor.Emitter[*pb.RawLedger]
}

func newOrigin(networkPassphrase string) *origin {
	return &origin{
		networkPassphrase: networkPassphrase,
		emitter:           processor.NewEmitter[*pb.RawLedger](1024),
	}
}

func (o *origin) Name() string         { return "stellar/raw-ledger" }
func (o *origin) Type() processor.Type { return processor.TypeOrigin }

// ProcessLedger converts one ledger and emits it. A marshal failure is
// reported as a per-ledger warning so the stream survives a single bad
// ledger, per the processor.Origin contract.
func (o *origin) ProcessLedger(ctx context.Context, ledger xdr.LedgerCloseMeta) {
	ev, err := rawledger.ToRawLedger(ledger, o.networkPassphrase)
	if err != nil {
		processor.ReportWarning(ctx, o.Name(), err)
		return
	}
	select {
	case <-ctx.Done():
	default:
		o.emitter.Emit(ev)
	}
}

func (o *origin) Out() <-chan *pb.RawLedger { return o.emitter.Out() }
func (o *origin) Close()                    { o.emitter.Close() }

func main() {
	config := cli.OriginConfig{
		Name:        "raw-ledger-origin",
		Description: "Emit raw Stellar LedgerCloseMeta XDR as JSONL events",
		Version:     "0.1.0",
		SchemaID:    "nebu.raw_ledger.v1",
	}

	cli.RunProtoOriginCLI(config, func(networkPass string) cli.ProtoOriginProcessor[*pb.RawLedger] {
		return newOrigin(networkPass)
	})
}
