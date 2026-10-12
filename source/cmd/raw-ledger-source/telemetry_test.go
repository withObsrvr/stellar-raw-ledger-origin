package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/ingest/ledgerbackend"
	flowctlv1 "github.com/withObsrvr/flow-proto/go/gen/flowctl/v1"
	"github.com/withObsrvr/flowctl-sdk/pkg/source"
	"github.com/withObsrvr/stellar-raw-ledger-origin/source/ledgerstream"
)

func TestStreamProgressAndMetrics(t *testing.T) {
	m := newStreamMonitor()
	events := []streamItem{{xdr: sourceFixtureLedgerXDR(t, 100, 1700000000)}, {err: errors.New("injected upstream failure")}}
	producer := newProducerWithMonitor(ledgerstream.Config{Type: ledgerstream.TypeArchive}, "testnet", "source", 100, 101,
		func(ledgerstream.Config) (ledgerbackend.LedgerStream, error) { return scriptedStream{events}, nil }, m)
	stream, err := producer(context.Background(), &flowctlv1.StreamRequest{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for range stream.Events {
		count++
	}
	if count != 1 {
		t.Fatalf("queued %d, want 1", count)
	}
	if err := <-stream.Result; err == nil {
		t.Fatal("expected upstream failure")
	}
	active, last := m.snapshot()
	if active != nil || last == nil || last.State != "error" || last.ErrorKind != source.UpstreamUnavailable || last.LastFetched != 100 || last.LastQueued != 100 || last.Expected != 101 || last.Component != "source" || last.ID == "" {
		t.Fatalf("progress = active=%+v last=%+v", active, last)
	}
	response := httptest.NewRecorder()
	m.handler().ServeHTTP(response, httptest.NewRequest("GET", "/status", nil))
	var snapshot struct {
		Last streamState `json:"last"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Last.LastQueued != 100 {
		t.Fatalf("status=%s", response.Body.String())
	}
	metrics := httptest.NewRecorder()
	m.handler().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	text, _ := io.ReadAll(metrics.Result().Body)
	if metrics.Code != 200 || !strings.Contains(string(text), `raw_ledger_source_ledgers_queued_total{backend="ARCHIVE"} 1`) || !strings.Contains(string(text), `raw_ledger_source_stream_errors_total{backend="ARCHIVE",error_kind="upstream_unavailable"} 1`) || strings.Contains(string(text), last.ID) {
		t.Fatalf("metrics = %s", text)
	}
}

func TestMonitorCanceledNotError(t *testing.T) {
	m := newStreamMonitor()
	m.start("one", "source", ledgerstream.TypeRPC, ledgerRange{start: 1})
	m.finish("one", context.Canceled)
	_, last := m.snapshot()
	if last == nil || last.State != "canceled" || last.ErrorKind != "" {
		t.Fatalf("canceled=%+v", last)
	}
}
