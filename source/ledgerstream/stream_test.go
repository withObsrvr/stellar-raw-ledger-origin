package ledgerstream

import "testing"

func TestConfigFromEnvStrict(t *testing.T) {
	t.Setenv("BACKEND_TYPE", "RPC")
	t.Setenv("RPC_ENDPOINT", "http://localhost:8000")
	if _, err := ConfigFromEnvStrict(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LEDGERS_PER_FILE", "FILES_PER_PARTITION", "BUFFER_SIZE", "NUM_WORKERS"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "fast")
			if _, err := ConfigFromEnvStrict(); err == nil {
				t.Fatal("invalid numeric value accepted")
			}
		})
	}
	t.Setenv("BACKEND_TYPE", "typo")
	if _, err := ConfigFromEnvStrict(); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestConfigFromEnvArchiveDefaults(t *testing.T) {
	t.Setenv("BACKEND_TYPE", "archive")
	t.Setenv("ARCHIVE_STORAGE_TYPE", "gcs")
	t.Setenv("ARCHIVE_BUCKET_NAME", "stellar-ledgers")

	cfg := ConfigFromEnv()
	if cfg.Type != TypeArchive {
		t.Fatalf("Type = %q, want %q", cfg.Type, TypeArchive)
	}
	if cfg.LedgersPerFile != 1 {
		t.Errorf("LedgersPerFile = %d, want 1", cfg.LedgersPerFile)
	}
	if cfg.FilesPerPartition != 64000 {
		t.Errorf("FilesPerPartition = %d, want 64000", cfg.FilesPerPartition)
	}
}

func TestConfigFromEnvArchiveOverrides(t *testing.T) {
	t.Setenv("BACKEND_TYPE", "archive")
	t.Setenv("ARCHIVE_STORAGE_TYPE", "s3")
	t.Setenv("ARCHIVE_BUCKET_NAME", "stellar-ledgers")
	t.Setenv("ARCHIVE_PATH", "landing/ledgers/testnet")
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("S3_ENDPOINT_URL", "http://localhost:9000")
	t.Setenv("LEDGERS_PER_FILE", "2")
	t.Setenv("FILES_PER_PARTITION", "32000")
	t.Setenv("BUFFER_SIZE", "7")
	t.Setenv("NUM_WORKERS", "3")

	cfg := ConfigFromEnv()
	if cfg.ArchiveStorageType != "s3" || cfg.ArchiveBucketName != "stellar-ledgers" || cfg.ArchivePath != "landing/ledgers/testnet" {
		t.Fatalf("archive config = storage=%q bucket=%q path=%q", cfg.ArchiveStorageType, cfg.ArchiveBucketName, cfg.ArchivePath)
	}
	if cfg.AWSRegion != "us-east-2" || cfg.S3EndpointURL != "http://localhost:9000" {
		t.Fatalf("s3 config = region=%q endpoint=%q", cfg.AWSRegion, cfg.S3EndpointURL)
	}
	if cfg.LedgersPerFile != 2 || cfg.FilesPerPartition != 32000 || cfg.BufferSize != 7 || cfg.NumWorkers != 3 {
		t.Fatalf("schema/buffer config = ledgers=%d files=%d buffer=%d workers=%d", cfg.LedgersPerFile, cfg.FilesPerPartition, cfg.BufferSize, cfg.NumWorkers)
	}
}

func TestConfigFromEnvLegacyStorageTypeSelectsArchive(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "GCS")
	t.Setenv("BUCKET_NAME", "legacy-bucket")
	t.Setenv("BUCKET_PATH", "legacy/path")

	cfg := ConfigFromEnv()
	if cfg.Type != TypeArchive {
		t.Fatalf("Type = %q, want %q", cfg.Type, TypeArchive)
	}
	if cfg.ArchiveStorageType != "GCS" || cfg.ArchiveBucketName != "legacy-bucket" || cfg.ArchivePath != "legacy/path" {
		t.Fatalf("legacy archive config = storage=%q bucket=%q path=%q", cfg.ArchiveStorageType, cfg.ArchiveBucketName, cfg.ArchivePath)
	}
}

func TestConfigFromEnvCaptiveCore(t *testing.T) {
	t.Setenv("BACKEND_TYPE", "captive_core")
	t.Setenv("NETWORK_PASSPHRASE", "Test SDF Network ; September 2015")
	t.Setenv("HISTORY_ARCHIVE_URLS", "https://history1.example, https://history2.example,,")
	t.Setenv("STELLAR_CORE_BINARY_PATH", "/usr/bin/stellar-core")
	t.Setenv("STELLAR_CORE_CONFIG_PATH", "/etc/stellar-core.cfg")
	t.Setenv("CAPTIVE_CORE_STORAGE_PATH", "/tmp/captive-core")

	cfg := ConfigFromEnv()
	if cfg.Type != TypeCaptiveCore {
		t.Fatalf("Type = %q, want %q", cfg.Type, TypeCaptiveCore)
	}
	if cfg.NetworkPassphrase != "Test SDF Network ; September 2015" {
		t.Fatalf("NetworkPassphrase = %q", cfg.NetworkPassphrase)
	}
	if len(cfg.HistoryArchiveURLs) != 2 || cfg.HistoryArchiveURLs[0] != "https://history1.example" || cfg.HistoryArchiveURLs[1] != "https://history2.example" {
		t.Fatalf("HistoryArchiveURLs = %#v", cfg.HistoryArchiveURLs)
	}
}

func TestArchiveBucketPath(t *testing.T) {
	tests := []struct {
		bucket string
		prefix string
		want   string
	}{
		{bucket: "bucket", want: "bucket"},
		{bucket: "bucket/", prefix: "/prefix/path/", want: "bucket/prefix/path"},
		{bucket: "bucket/base", prefix: "prefix", want: "bucket/base/prefix"},
	}
	for _, test := range tests {
		if got := archiveBucketPath(test.bucket, test.prefix); got != test.want {
			t.Errorf("archiveBucketPath(%q, %q) = %q, want %q", test.bucket, test.prefix, got, test.want)
		}
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "unknown type", cfg: Config{Type: "unknown"}},
		{name: "RPC endpoint", cfg: Config{Type: TypeRPC}},
		{name: "archive location", cfg: Config{Type: TypeArchive}},
		{name: "captive binary", cfg: Config{Type: TypeCaptiveCore}},
		{
			name: "unsupported archive type",
			cfg: Config{
				Type:               TypeArchive,
				ArchiveStorageType: "FS",
				ArchiveBucketName:  "bucket",
				LedgersPerFile:     1,
				FilesPerPartition:  64000,
				BufferSize:         5,
				NumWorkers:         2,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
