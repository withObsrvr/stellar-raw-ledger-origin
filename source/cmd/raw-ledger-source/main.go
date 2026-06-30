// Command raw-ledger-source is the flowctl-style adapter for the shared
// raw-ledger core. It is a long-running source service: it selects an
// upstream Stellar backend, streams ledgers through the same
// rawledger.ToRawLedger core the nebu origin uses, and serves them as
// flowctl Events.
//
// Service lifecycle — control-plane registration, heartbeats, the health
// endpoint, the gRPC server, and graceful shutdown — is provided by
// flowctl-sdk. This file only wires configuration and the producer.
//
// Configuration (environment):
//
//	BACKEND_TYPE          RPC | ARCHIVE | CAPTIVE_CORE (default RPC)
//	RPC_ENDPOINT          Stellar RPC URL (RPC backend)
//	RPC_AUTH_HEADER       optional Authorization header
//	ARCHIVE_STORAGE_TYPE  GCS | S3 (ARCHIVE backend)
//	ARCHIVE_BUCKET_NAME   archive bucket name (ARCHIVE backend)
//	ARCHIVE_PATH          optional archive prefix inside bucket
//	LEDGERS_PER_FILE      archive schema ledgers per file (default 64)
//	FILES_PER_PARTITION   archive schema files per partition (default 10)
//	BUFFER_SIZE           archive read-ahead buffer size (default 5)
//	NUM_WORKERS           archive worker count (default 2)
//	AWS_REGION            S3 region (default us-east-1)
//	S3_ENDPOINT_URL       optional S3-compatible endpoint URL
//	HISTORY_ARCHIVE_URLS  comma-separated archive URLs (CAPTIVE_CORE backend)
//	STELLAR_CORE_BINARY_PATH stellar-core binary path (CAPTIVE_CORE backend)
//	STELLAR_CORE_CONFIG_PATH optional base stellar-core config file (CAPTIVE_CORE backend)
//	CAPTIVE_CORE_STORAGE_PATH optional bucket storage base path (CAPTIVE_CORE backend)
//	NETWORK_PASSPHRASE    network passphrase (default pubnet)
//	START_LEDGER        default start ledger if a stream omits start_ledger
//	END_LEDGER          default end ledger (0 = unbounded)
//	GRPC_PORT           gRPC listen port (default 50051)
//	HEALTH_PORT         health endpoint port (default 8088)
//	FLOWCTL_ENDPOINT      control plane address; if set, registration is enabled
//	FLOWCTL_COMPONENT_ID  flowctl-assigned component id (preferred)
//	SOURCE_ID             component id fallback (default raw-ledger-source)
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/withObsrvr/flowctl-sdk/pkg/source"

	"github.com/withObsrvr/stellar-raw-ledger-origin/source/internal/backend"
)

const version = "0.1.0"

func main() {
	networkPassphrase := envOr("NETWORK_PASSPHRASE", network.PublicNetworkPassphrase)
	beCfg := backend.ConfigFromEnv()
	defStart := envUint32("START_LEDGER", 0)
	defEnd := envUint32("END_LEDGER", 0)

	cfg := source.DefaultConfig()
	cfg.ID = componentIDFromEnv()
	cfg.Name = "raw-ledger-source"
	cfg.Description = "Streams raw Stellar ledgers (stellar.v1.RawLedger) from a configurable backend"
	cfg.Version = version
	cfg.OutputEventTypes = []string{rawLedgerEventType}
	cfg.Endpoint = ":" + envOr("GRPC_PORT", "50051")
	cfg.HealthPort = int(envUint32("HEALTH_PORT", 8088))

	// Enable control-plane registration only when an endpoint is provided,
	// so the service runs standalone for local testing without flowctl.
	if ep := os.Getenv("FLOWCTL_ENDPOINT"); ep != "" {
		cfg.FlowctlConfig.Enabled = true
		cfg.FlowctlConfig.Endpoint = ep
	}

	src, err := source.New(cfg)
	if err != nil {
		log.Fatalf("create source: %v", err)
	}

	if err := src.OnProduce(newProducer(beCfg, networkPassphrase, cfg.ID, defStart, defEnd)); err != nil {
		log.Fatalf("register producer: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := src.Start(ctx); err != nil {
		log.Fatalf("start source: %v", err)
	}
	log.Printf("raw-ledger-source %s listening on %s (health :%d, backend %s)",
		version, cfg.Endpoint, cfg.HealthPort, beCfg.Type)

	<-ctx.Done()
	log.Println("shutting down...")
	if err := src.Stop(); err != nil {
		log.Fatalf("stop source: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func componentIDFromEnv() string {
	// flowctl injects FLOWCTL_COMPONENT_ID from the pipeline component id.
	// Prefer it so the control plane sees the id it is waiting for; SOURCE_ID
	// remains a standalone/local fallback.
	return envOr("FLOWCTL_COMPONENT_ID", envOr("SOURCE_ID", "raw-ledger-source"))
}

func envUint32(key string, fallback uint32) uint32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		log.Fatalf("invalid %s %q: %v", key, v, err)
	}
	return uint32(n)
}
