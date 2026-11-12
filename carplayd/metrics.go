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
// metrics is carplayd's state as Prometheus text. Per car, not per box: with a
// radio per car several links serve at once, and a single "serving" gauge would
// show whichever link wrote last.
type metrics struct {
	mu       sync.Mutex
	cars     map[string]*carMetric
	upstream bool
	counters map[string]int // joins, leaves, goodbyes, link_lost, switches, wpa_restarts
}

type carMetric struct {
	serving bool
	signal  int
	phones  int
	detect  float64 // seconds from first scan sighting to connected, last time
	// probeBytes is iOS's http://captive.apple.com validation fetch, which is
	// the only thing that distinguishes "a phone is on our prefix" from "a
	// phone will actually route app traffic through us". fwdBytes is everything
	// forwarded from the car prefix, for context. See daemon.checkValidated.
	probeBytes int64
	fwdBytes   int64
}

func newMetrics() *metrics {
	return &metrics{cars: map[string]*carMetric{}, counters: map[string]int{}}
}

// car returns a car's slot, creating it. Call with mu held.
func (m *metrics) car(id string) *carMetric {
	c := m.cars[id]
	if c == nil {
		c = &carMetric{}
		m.cars[id] = c
	}
	return c
}

// setCar mutates one car's metrics under the lock.
func (m *metrics) setCar(id string, f func(*carMetric)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m.car(id))
}

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
	ids := make([]string, 0, len(m.cars))
	for id := range m.cars {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Fprintf(w, "# HELP carplayd_serving 1 while serving a car.\n# TYPE carplayd_serving gauge\n")
	for _, id := range ids {
		v := 0
		if m.cars[id].serving {
			v = 1
		}
		fmt.Fprintf(w, "carplayd_serving{car=%q} %d\n", id, v)
	}
	fmt.Fprintf(w, "# TYPE carplayd_signal_dbm gauge\n")
	for _, id := range ids {
		fmt.Fprintf(w, "carplayd_signal_dbm{car=%q} %d\n", id, m.cars[id].signal)
	}
	fmt.Fprintf(w, "# TYPE carplayd_phones gauge\n")
	for _, id := range ids {
		fmt.Fprintf(w, "carplayd_phones{car=%q} %d\n", id, m.cars[id].phones)
	}
	up := 0
	if m.upstream {
		up = 1
	}
	fmt.Fprintf(w, "# HELP carplayd_upstream 1 while the internet is reachable through the uplink.\n# TYPE carplayd_upstream gauge\ncarplayd_upstream %d\n", up)
	fmt.Fprintf(w, "# HELP carplayd_phone_probe_bytes iOS internet-validation fetches from the car prefix. Zero means apps will not use this link.\n# TYPE carplayd_phone_probe_bytes counter\n")
	for _, id := range ids {
		fmt.Fprintf(w, "carplayd_phone_probe_bytes{car=%q} %d\n", id, m.cars[id].probeBytes)
	}
	fmt.Fprintf(w, "# TYPE carplayd_phone_forward_bytes counter\n")
	for _, id := range ids {
		fmt.Fprintf(w, "carplayd_phone_forward_bytes{car=%q} %d\n", id, m.cars[id].fwdBytes)
	}
	fmt.Fprintf(w, "# TYPE carplayd_detect_seconds gauge\n")
	for _, id := range ids {
		fmt.Fprintf(w, "carplayd_detect_seconds{car=%q} %.3f\n", id, m.cars[id].detect)
	}
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
