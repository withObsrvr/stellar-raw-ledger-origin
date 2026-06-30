// Package backend selects and constructs the upstream Stellar ledger
// source for the flowctl raw-ledger-source service.
//
// All backends satisfy the Stellar SDK's ledgerbackend.LedgerBackend
// interface, so the producer in cmd/raw-ledger-source is backend-agnostic:
// it only calls PrepareRange / GetLedger / Close.
package backend

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/support/datastore"
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

	// Archive/datalake backend.
	ArchiveStorageType string
	ArchiveBucketName  string
	ArchivePath        string
	AWSRegion          string
	S3EndpointURL      string
	LedgersPerFile     uint32
	FilesPerPartition  uint32
	BufferSize         uint32
	NumWorkers         uint32

	// Captive Core backend.
	NetworkPassphrase      string
	HistoryArchiveURLs     []string
	StellarCoreBinaryPath  string
	StellarCoreConfigPath  string
	CaptiveCoreStoragePath string
}

// ConfigFromEnv reads backend configuration from the environment.
//
//	BACKEND_TYPE          RPC | ARCHIVE | CAPTIVE_CORE (default RPC)
//	RPC_ENDPOINT          Stellar RPC URL (required for RPC)
//	RPC_AUTH_HEADER       optional Authorization header value
//	ARCHIVE_STORAGE_TYPE  GCS | S3 (required for ARCHIVE; STORAGE_TYPE fallback)
//	ARCHIVE_BUCKET_NAME   bucket name (required for ARCHIVE; BUCKET_NAME fallback)
//	ARCHIVE_PATH          optional prefix inside the bucket (BUCKET_PATH fallback)
//	LEDGERS_PER_FILE      archive schema ledgers per file (default 64)
//	FILES_PER_PARTITION   archive schema files per partition (default 10)
//	BUFFER_SIZE           archive read-ahead buffer size (default 5)
//	NUM_WORKERS           archive worker count (default 2)
//	AWS_REGION            S3 region (default us-east-1)
//	S3_ENDPOINT_URL       optional S3-compatible endpoint URL
//	NETWORK_PASSPHRASE    network passphrase for CAPTIVE_CORE (default pubnet)
//	HISTORY_ARCHIVE_URLS  comma-separated archive URLs for CAPTIVE_CORE
//	STELLAR_CORE_BINARY_PATH  stellar-core binary path for CAPTIVE_CORE
//	STELLAR_CORE_CONFIG_PATH  optional base stellar-core config file for CAPTIVE_CORE
//	CAPTIVE_CORE_STORAGE_PATH optional captive-core bucket storage base path
func ConfigFromEnv() Config {
	t := Type(strings.ToUpper(os.Getenv("BACKEND_TYPE")))
	if t == "" {
		if storageType := strings.ToUpper(os.Getenv("STORAGE_TYPE")); storageType == "GCS" || storageType == "S3" {
			t = TypeArchive
		} else {
			t = TypeRPC
		}
	}
	return Config{
		Type:                   t,
		RPCServerURL:           os.Getenv("RPC_ENDPOINT"),
		RPCAuthHeader:          os.Getenv("RPC_AUTH_HEADER"),
		ArchiveStorageType:     envOr("ARCHIVE_STORAGE_TYPE", os.Getenv("STORAGE_TYPE")),
		ArchiveBucketName:      envOr("ARCHIVE_BUCKET_NAME", os.Getenv("BUCKET_NAME")),
		ArchivePath:            envOr("ARCHIVE_PATH", os.Getenv("BUCKET_PATH")),
		AWSRegion:              envOr("AWS_REGION", "us-east-1"),
		S3EndpointURL:          os.Getenv("S3_ENDPOINT_URL"),
		LedgersPerFile:         envUint32("LEDGERS_PER_FILE", 64),
		FilesPerPartition:      envUint32("FILES_PER_PARTITION", 10),
		BufferSize:             envUint32("BUFFER_SIZE", 5),
		NumWorkers:             envUint32("NUM_WORKERS", 2),
		NetworkPassphrase:      envOr("NETWORK_PASSPHRASE", network.PublicNetworkPassphrase),
		HistoryArchiveURLs:     csvList(os.Getenv("HISTORY_ARCHIVE_URLS")),
		StellarCoreBinaryPath:  os.Getenv("STELLAR_CORE_BINARY_PATH"),
		StellarCoreConfigPath:  os.Getenv("STELLAR_CORE_CONFIG_PATH"),
		CaptiveCoreStoragePath: os.Getenv("CAPTIVE_CORE_STORAGE_PATH"),
	}
}

// New constructs the configured ledger backend.
func New(cfg Config) (ledgerbackend.LedgerBackend, error) {
	switch cfg.Type {
	case TypeRPC:
		return newRPC(cfg)
	case TypeArchive:
		return newArchive(context.Background(), cfg)
	case TypeCaptiveCore:
		return newCaptiveCore(context.Background(), cfg)
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

func newArchive(ctx context.Context, cfg Config) (ledgerbackend.LedgerBackend, error) {
	storageType := strings.ToUpper(cfg.ArchiveStorageType)
	if storageType == "" || cfg.ArchiveBucketName == "" {
		return nil, fmt.Errorf("ARCHIVE backend requires ARCHIVE_STORAGE_TYPE (or STORAGE_TYPE) and ARCHIVE_BUCKET_NAME (or BUCKET_NAME)")
	}

	schema := datastore.DataStoreSchema{
		LedgersPerFile:    cfg.LedgersPerFile,
		FilesPerPartition: cfg.FilesPerPartition,
	}
	if schema.LedgersPerFile == 0 {
		return nil, fmt.Errorf("LEDGERS_PER_FILE must be > 0")
	}
	if schema.FilesPerPartition == 0 {
		return nil, fmt.Errorf("FILES_PER_PARTITION must be > 0")
	}

	params := map[string]string{
		"destination_bucket_path": archiveBucketPath(cfg.ArchiveBucketName, cfg.ArchivePath),
	}
	dsType := storageType
	switch storageType {
	case "GCS":
		// destination_bucket_path is enough.
	case "S3":
		params["region"] = cfg.AWSRegion
		if cfg.S3EndpointURL != "" {
			params["endpoint_url"] = cfg.S3EndpointURL
		}
	default:
		return nil, fmt.Errorf("unsupported ARCHIVE_STORAGE_TYPE %q (want GCS or S3)", cfg.ArchiveStorageType)
	}

	dataStore, err := datastore.NewDataStore(ctx, datastore.DataStoreConfig{
		Type:   dsType,
		Schema: schema,
		Params: params,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s datastore %q: %w", dsType, params["destination_bucket_path"], err)
	}

	bufferSize := cfg.BufferSize
	if bufferSize == 0 {
		return nil, fmt.Errorf("BUFFER_SIZE must be > 0")
	}
	numWorkers := cfg.NumWorkers
	if numWorkers == 0 {
		return nil, fmt.Errorf("NUM_WORKERS must be > 0")
	}
	be, err := ledgerbackend.NewBufferedStorageBackend(ledgerbackend.BufferedStorageBackendConfig{
		BufferSize: bufferSize,
		NumWorkers: numWorkers,
		RetryLimit: 3,
		RetryWait:  time.Second,
	}, dataStore, schema)
	if err != nil {
		dataStore.Close()
		return nil, fmt.Errorf("create buffered storage backend: %w", err)
	}
	return be, nil
}

func newCaptiveCore(ctx context.Context, cfg Config) (ledgerbackend.LedgerBackend, error) {
	if cfg.StellarCoreBinaryPath == "" {
		return nil, fmt.Errorf("CAPTIVE_CORE backend requires STELLAR_CORE_BINARY_PATH")
	}
	if cfg.NetworkPassphrase == "" {
		return nil, fmt.Errorf("CAPTIVE_CORE backend requires NETWORK_PASSPHRASE")
	}
	if len(cfg.HistoryArchiveURLs) == 0 {
		return nil, fmt.Errorf("CAPTIVE_CORE backend requires HISTORY_ARCHIVE_URLS")
	}

	tomlParams := ledgerbackend.CaptiveCoreTomlParams{
		NetworkPassphrase:  cfg.NetworkPassphrase,
		HistoryArchiveURLs: cfg.HistoryArchiveURLs,
		CoreBinaryPath:     cfg.StellarCoreBinaryPath,
	}

	var (
		captiveCoreToml *ledgerbackend.CaptiveCoreToml
		err             error
	)
	if cfg.StellarCoreConfigPath != "" {
		captiveCoreToml, err = ledgerbackend.NewCaptiveCoreTomlFromFile(cfg.StellarCoreConfigPath, tomlParams)
	} else {
		captiveCoreToml, err = ledgerbackend.NewCaptiveCoreToml(tomlParams)
	}
	if err != nil {
		return nil, fmt.Errorf("create captive core toml: %w", err)
	}

	return ledgerbackend.NewCaptive(ledgerbackend.CaptiveCoreConfig{
		BinaryPath:          cfg.StellarCoreBinaryPath,
		NetworkPassphrase:   cfg.NetworkPassphrase,
		HistoryArchiveURLs:  cfg.HistoryArchiveURLs,
		CheckpointFrequency: 64,
		Toml:                captiveCoreToml,
		Context:             ctx,
		StoragePath:         cfg.CaptiveCoreStoragePath,
	})
}

func archiveBucketPath(bucket, prefix string) string {
	bucket = strings.Trim(bucket, "/")
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return bucket
	}
	return bucket + "/" + prefix
}

func csvList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envUint32(key string, fallback uint32) uint32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return fallback
	}
	return uint32(n)
}

// authTransport injects a static Authorization header on every RPC
// request (for authenticated RPC providers).
type authTransport struct {
	base       http.RoundTripper
	authHeader string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", t.authHeader)
	return t.base.RoundTrip(req)
}
