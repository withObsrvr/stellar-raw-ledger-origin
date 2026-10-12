package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/withObsrvr/flowctl-sdk/pkg/source"
	"github.com/withObsrvr/stellar-raw-ledger-origin/source/ledgerstream"
)

// streamMonitor records source-side progress only. A queued event may not have
// been sent by the SDK, received by a consumer, or committed by an ingester.
// Stream identifiers, network passphrases and error text never become labels.
type streamMonitor struct {
	mu           sync.RWMutex
	active       *streamState
	last         streamState
	registry     *prometheus.Registry
	activeGauge  prometheus.Gauge
	total        *prometheus.CounterVec
	errors       *prometheus.CounterVec
	fetched      *prometheus.CounterVec
	queued       *prometheus.CounterVec
	lastFetched  *prometheus.GaugeVec
	lastQueued   *prometheus.GaugeVec
	lastQueuedAt *prometheus.GaugeVec
}

type streamState struct {
	ID           string            `json:"stream_id"`
	Component    string            `json:"component_id"`
	Backend      ledgerstream.Type `json:"backend"`
	Start        uint32            `json:"requested_start_ledger"`
	End          uint32            `json:"requested_end_ledger"`
	Expected     uint32            `json:"expected_ledger"`
	Started      time.Time         `json:"started_at"`
	LastFetched  uint32            `json:"last_fetched_ledger"`
	LastQueued   uint32            `json:"last_queued_ledger"`
	LastQueuedAt time.Time         `json:"last_queued_at,omitempty"`
	State        string            `json:"state"`
	ErrorKind    source.ErrorKind  `json:"error_kind,omitempty"`
}

func newStreamMonitor() *streamMonitor {
	m := &streamMonitor{
		registry:     prometheus.NewRegistry(),
		activeGauge:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "raw_ledger_source_streams_active", Help: "Active source streams."}),
		total:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "raw_ledger_source_streams_total", Help: "Completed streams by result."}, []string{"result"}),
		errors:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "raw_ledger_source_stream_errors_total", Help: "Terminal source errors by backend and kind."}, []string{"backend", "error_kind"}),
		fetched:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "raw_ledger_source_ledgers_fetched_total", Help: "Validated XDR ledgers fetched."}, []string{"backend"}),
		queued:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "raw_ledger_source_ledgers_queued_total", Help: "Events queued for the SDK (not delivered or committed)."}, []string{"backend"}),
		lastFetched:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "raw_ledger_source_last_fetched_ledger", Help: "Last validated fetched ledger (in this process)."}, []string{"backend"}),
		lastQueued:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "raw_ledger_source_last_queued_ledger", Help: "Last ledger queued to the SDK (in this process)."}, []string{"backend"}),
		lastQueuedAt: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "raw_ledger_source_last_queued_timestamp_seconds", Help: "Unix timestamp of last queued ledger."}, []string{"backend"}),
	}
	m.registry.MustRegister(m.activeGauge, m.total, m.errors, m.fetched, m.queued, m.lastFetched, m.lastQueued, m.lastQueuedAt)
	return m
}

func (m *streamMonitor) start(id, component string, backend ledgerstream.Type, r ledgerRange) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = &streamState{ID: id, Component: component, Backend: backend, Start: r.start, End: r.end, Expected: r.start, Started: time.Now(), State: "working"}
	m.activeGauge.Set(1)
}
func (m *streamMonitor) fetch(id string, seq uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.ID != id {
		return
	}
	m.active.LastFetched = seq
	m.fetched.WithLabelValues(string(m.active.Backend)).Inc()
	m.lastFetched.WithLabelValues(string(m.active.Backend)).Set(float64(seq))
}
func (m *streamMonitor) queue(id string, seq uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.ID != id {
		return
	}
	m.active.LastQueued = seq
	if seq != ^uint32(0) {
		m.active.Expected = seq + 1
	}
	m.active.LastQueuedAt = time.Now()
	m.queued.WithLabelValues(string(m.active.Backend)).Inc()
	m.lastQueued.WithLabelValues(string(m.active.Backend)).Set(float64(seq))
	m.lastQueuedAt.WithLabelValues(string(m.active.Backend)).Set(float64(m.active.LastQueuedAt.Unix()))
}
func (m *streamMonitor) finish(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.ID != id {
		return
	}
	s := *m.active
	switch {
	case errors.Is(err, context.Canceled):
		s.State = "canceled"
	case err == nil:
		s.State = "done"
	default:
		s.State = "error"
		var classified *source.StreamError
		if errors.As(err, &classified) {
			s.ErrorKind = classified.Kind
		} else {
			s.ErrorKind = source.Internal
		}
		m.errors.WithLabelValues(string(s.Backend), string(s.ErrorKind)).Inc()
	}
	m.total.WithLabelValues(s.State).Inc()
	m.last = s
	m.active = nil
	m.activeGauge.Set(0)
}
func (m *streamMonitor) snapshot() (active *streamState, last *streamState) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active != nil {
		copy := *m.active
		active = &copy
	}
	if m.last.ID != "" {
		copy := m.last
		last = &copy
	}
	return
}

// Prometheus is served on a separate port: the SDK owns HEALTH_PORT and its
// /metrics endpoint is JSON. This handler does not change SDK health semantics.
func (m *streamMonitor) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		active, last := m.snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Active *streamState `json:"active"`
			Last   *streamState `json:"last"`
		}{active, last})
	})
	return mux
}
func (m *streamMonitor) serve(port uint16) (*http.Server, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: m.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return server, nil
}
