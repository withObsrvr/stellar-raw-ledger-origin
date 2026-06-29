// Package backend selects and constructs the upstream Stellar ledger
// source for the flowctl raw-ledger-source service.
//
// All backends satisfy the Stellar SDK's ledgerbackend.LedgerBackend
// interface, so the producer in cmd/raw-ledger-source is backend-agnostic:
// it only calls PrepareRange / GetLedger / Close.
package backend

import (
	"fmt"
	"net/http"
	"os"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
)

// Type enumerates the supported upstream backends.
type Type string

const (
	TypeRPC         Type = "RPC"
	TypeArchive     Type = "ARCHIVE"
	TypeCaptiveCore Type = "CAPTIVE_CORE"
)

// Config is the backend selection + connection configuration.
type Config struct {
	Type Type

	// RPC backend.
	RPCServerURL  string
	RPCAuthHeader string
}

// ConfigFromEnv reads backend configuration from the environment.
//
//	BACKEND_TYPE     RPC | ARCHIVE | CAPTIVE_CORE (default RPC)
//	RPC_ENDPOINT     Stellar RPC URL (required for RPC)
//	RPC_AUTH_HEADER  optional Authorization header value
func ConfigFromEnv() Config {
	t := Type(os.Getenv("BACKEND_TYPE"))
	if t == "" {
		t = TypeRPC
	}
	return Config{
		Type:          t,
		RPCServerURL:  os.Getenv("RPC_ENDPOINT"),
		RPCAuthHeader: os.Getenv("RPC_AUTH_HEADER"),
	}
}

// New constructs the configured ledger backend.
//
// Only the RPC backend is wired in this milestone. The ARCHIVE and
// CAPTIVE_CORE cases are deliberate, explicit seams: the switch is the
// single place to add them (see the stellar-live-source-datalake
// reference and docs/HANDOFF-milestone-2.md), and they fail loudly rather
// than silently until then.
func New(cfg Config) (ledgerbackend.LedgerBackend, error) {
	switch cfg.Type {
	case TypeRPC:
		return newRPC(cfg)
	case TypeArchive, TypeCaptiveCore:
		return nil, fmt.Errorf("backend %q not yet implemented (RPC only in this milestone)", cfg.Type)
	default:
		return nil, fmt.Errorf("unknown BACKEND_TYPE %q (want RPC, ARCHIVE, or CAPTIVE_CORE)", cfg.Type)
	}
}

func newRPC(cfg Config) (ledgerbackend.LedgerBackend, error) {
	if cfg.RPCServerURL == "" {
		return nil, fmt.Errorf("RPC backend requires RPC_ENDPOINT")
	}
	opts := ledgerbackend.RPCLedgerBackendOptions{
		RPCServerURL: cfg.RPCServerURL,
		BufferSize:   10,
	}
	if cfg.RPCAuthHeader != "" {
		opts.HttpClient = &http.Client{
			Transport: &authTransport{base: http.DefaultTransport, authHeader: cfg.RPCAuthHeader},
		}
	}
	return ledgerbackend.NewRPCLedgerBackend(opts), nil
}

// authTransport injects a static Authorization header on every RPC
// request (for authenticated RPC providers).
type authTransport struct {
	base       http.RoundTripper
	authHeader string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", t.authHeader)
	return t.base.RoundTrip(req)
}
