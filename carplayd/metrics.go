package main

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// metrics is carplayd's state as Prometheus text, for the lab phase.
type metrics struct {
	mu       sync.Mutex
	serving  string // car ID or ""
	signal   int
	phones   int
	counters map[string]int // joins, leaves, goodbyes, link_lost, switches, wpa_restarts
	detect   float64        // seconds from first scan sighting to connected, last time
}

func newMetrics() *metrics { return &metrics{counters: map[string]int{}} }

func (m *metrics) inc(name string) {
	m.mu.Lock()
	m.counters[name]++
	m.mu.Unlock()
}

func (m *metrics) set(f func(m *metrics)) {
	m.mu.Lock()
	f(m)
	m.mu.Unlock()
}

func (m *metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	serving := 0
	if m.serving != "" {
		serving = 1
	}
	fmt.Fprintf(w, "# HELP carplayd_serving 1 while serving a car.\n# TYPE carplayd_serving gauge\ncarplayd_serving{car=%q} %d\n", m.serving, serving)
	fmt.Fprintf(w, "# TYPE carplayd_signal_dbm gauge\ncarplayd_signal_dbm %d\n", m.signal)
	fmt.Fprintf(w, "# TYPE carplayd_phones gauge\ncarplayd_phones %d\n", m.phones)
	fmt.Fprintf(w, "# TYPE carplayd_detect_seconds gauge\ncarplayd_detect_seconds %.3f\n", m.detect)
	names := make([]string, 0, len(m.counters))
	for k := range m.counters {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "# TYPE carplayd_events_total counter\n")
	for _, k := range names {
		fmt.Fprintf(w, "carplayd_events_total{event=%q} %d\n", k, m.counters[k])
	}
}

// listen binds first (so a taken port is reported, not silently ignored),
// then serves in the background.
func (m *metrics) listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: m, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	return nil
}
