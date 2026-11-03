package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// wpaConn speaks wpa_supplicant's control interface (UNIX datagram sockets,
// one request -> one reply; "<N>"-prefixed lines are unsolicited events).
type wpaConn struct {
	mu    sync.Mutex
	c     *net.UnixConn
	local string
}

var wpaSeq int
var wpaSeqMu sync.Mutex

func dialWPA(ctrlDir, iface string) (*wpaConn, error) {
	wpaSeqMu.Lock()
	wpaSeq++
	n := wpaSeq
	wpaSeqMu.Unlock()
	local := filepath.Join(os.TempDir(), fmt.Sprintf("carplayd-%d-%d", os.Getpid(), n))
	os.Remove(local)
	c, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: local, Net: "unixgram"},
		&net.UnixAddr{Name: filepath.Join(ctrlDir, iface), Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	return &wpaConn{c: c, local: local}, nil
}

func (w *wpaConn) close() {
	w.c.Close()
	os.Remove(w.local)
}

// cmd sends one command and returns its reply, skipping unsolicited events.
func (w *wpaConn) cmd(s string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.c.Write([]byte(s)); err != nil {
		return "", err
	}
	buf := make([]byte, 8192)
	deadline := time.Now().Add(3 * time.Second)
	for {
		w.c.SetReadDeadline(deadline)
		n, err := w.c.Read(buf)
		if err != nil {
			return "", fmt.Errorf("%s: %w", s, err)
		}
		r := string(buf[:n])
		if strings.HasPrefix(r, "<") {
			continue // event on a command socket; not ours
		}
		return r, nil
	}
}

// ok runs a command that should answer OK.
func (w *wpaConn) ok(s string) error {
	r, err := w.cmd(s)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r) != "OK" {
		return fmt.Errorf("%s: %s", s, strings.TrimSpace(r))
	}
	return nil
}

// events attaches and delivers event lines (without the "<N>" priority).
func (w *wpaConn) events(out chan<- string) error {
	if err := w.ok("ATTACH"); err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 8192)
		for {
			w.c.SetReadDeadline(time.Time{})
			n, err := w.c.Read(buf)
			if err != nil {
				close(out)
				return
			}
			l := string(buf[:n])
			if i := strings.Index(l, ">"); strings.HasPrefix(l, "<") && i > 0 {
				l = l[i+1:]
			}
			out <- l
		}
	}()
	return nil
}

// scanEntry is one line of SCAN_RESULTS.
type scanEntry struct {
	BSSID  string
	Freq   int
	Signal int
	Flags  string
	SSID   string
}

// parseScanResults parses "bssid / frequency / signal level / flags / ssid".
func parseScanResults(r string) []scanEntry {
	var out []scanEntry
	for i, l := range strings.Split(r, "\n") {
		if i == 0 || strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.SplitN(l, "\t", 5)
		if len(f) < 4 {
			continue
		}
		e := scanEntry{BSSID: strings.ToLower(f[0]), Flags: f[3]}
		e.Freq, _ = strconv.Atoi(f[1])
		e.Signal, _ = strconv.Atoi(f[2])
		if len(f) == 5 {
			e.SSID = f[4]
		}
		out = append(out, e)
	}
	return out
}

// parseKV parses "key=value" reply lines (STATUS, SIGNAL_POLL).
func parseKV(r string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(r, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			m[k] = v
		}
	}
	return m
}

// wpaConfig renders our private wpa_supplicant config: one network per car,
// all disabled; carplayd enables the one it picks.
func wpaConfig(cfg Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ctrl_interface=%s\nap_scan=1\nupdate_config=0\n", cfg.CtrlDir)
	for _, c := range cfg.Cars {
		fmt.Fprintf(&b, "network={\n\tid_str=%q\n\tssid=%q\n\tpsk=%q\n\tkey_mgmt=WPA-PSK\n\tscan_ssid=1\n\tscan_freq=%d\n\tfreq_list=%d\n\tbgscan=\"\"\n\tdisabled=1\n", c.ID, c.SSID, c.PSK, c.Freq(), c.Freq())
		if c.BSSID != "" {
			fmt.Fprintf(&b, "\tbssid=%s\n", c.BSSID)
		}
		b.WriteString("}\n")
	}
	return b.String()
}
