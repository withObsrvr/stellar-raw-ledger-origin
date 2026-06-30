package backend

import "testing"

func TestConfigFromEnvArchive(t *testing.T) {
	t.Setenv("BACKEND_TYPE", "archive")
	t.Setenv("ARCHIVE_STORAGE_TYPE", "s3")
	t.Setenv("ARCHIVE_BUCKET_NAME", "stellar-ledgers")
	t.Setenv("ARCHIVE_PATH", "landing/ledgers/testnet")
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("S3_ENDPOINT_URL", "http://localhost:9000")
	t.Setenv("LEDGERS_PER_FILE", "1")
	t.Setenv("FILES_PER_PARTITION", "64000")
	t.Setenv("BUFFER_SIZE", "7")
	t.Setenv("NUM_WORKERS", "3")

	cfg := ConfigFromEnv()
	if cfg.Type != TypeArchive {
		t.Fatalf("Type = %q, want %q", cfg.Type, TypeArchive)
	}
	if cfg.ArchiveStorageType != "s3" || cfg.ArchiveBucketName != "stellar-ledgers" || cfg.ArchivePath != "landing/ledgers/testnet" {
		t.Fatalf("archive config = storage=%q bucket=%q path=%q", cfg.ArchiveStorageType, cfg.ArchiveBucketName, cfg.ArchivePath)
	}
	if cfg.AWSRegion != "us-east-2" || cfg.S3EndpointURL != "http://localhost:9000" {
		t.Fatalf("s3 config = region=%q endpoint=%q", cfg.AWSRegion, cfg.S3EndpointURL)
	}
	if cfg.LedgersPerFile != 1 || cfg.FilesPerPartition != 64000 || cfg.BufferSize != 7 || cfg.NumWorkers != 3 {
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
	if cfg.StellarCoreBinaryPath != "/usr/bin/stellar-core" || cfg.StellarCoreConfigPath != "/etc/stellar-core.cfg" || cfg.CaptiveCoreStoragePath != "/tmp/captive-core" {
		t.Fatalf("captive config = binary=%q config=%q storage=%q", cfg.StellarCoreBinaryPath, cfg.StellarCoreConfigPath, cfg.CaptiveCoreStoragePath)
	}
}

func TestArchiveBucketPath(t *testing.T) {
	tests := []struct {
		bucket, prefix string
		want           string
	}{
		{"bucket", "", "bucket"},
		{"bucket/", "/prefix/path/", "bucket/prefix/path"},
		{"bucket/base", "prefix", "bucket/base/prefix"},
	}
	for _, tt := range tests {
		if got := archiveBucketPath(tt.bucket, tt.prefix); got != tt.want {
			t.Errorf("archiveBucketPath(%q, %q) = %q, want %q", tt.bucket, tt.prefix, got, tt.want)
		}
	}
}

func TestCSVList(t *testing.T) {
	got := csvList(" a, ,b,, c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("csvList length = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("csvList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewArchiveValidation(t *testing.T) {
	_, err := New(Config{Type: TypeArchive})
	if err == nil {
		t.Fatal("expected missing archive config error")
	}

	_, err = New(Config{
		Type:               TypeArchive,
		ArchiveStorageType: "FS",
		ArchiveBucketName:  "bucket",
		LedgersPerFile:     64,
		FilesPerPartition:  10,
		BufferSize:         5,
		NumWorkers:         2,
	})
	if err == nil {
		t.Fatal("expected unsupported storage type error")
	}
}

func TestNewCaptiveCoreValidation(t *testing.T) {
	_, err := New(Config{Type: TypeCaptiveCore})
	if err == nil {
		t.Fatal("expected missing stellar-core binary error")
	}

	_, err = New(Config{
		Type:                  TypeCaptiveCore,
		StellarCoreBinaryPath: "/does/not/exist/stellar-core",
		NetworkPassphrase:     "Test SDF Network ; September 2015",
	})
	if err == nil {
		t.Fatal("expected missing history archives error")
	}
}
