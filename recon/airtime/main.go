// airtime answers the question byte counters cannot: how much of the car's
// Wi-Fi channel is left after CarPlay has taken what it needs.
//
//	sudo airtime -iface mon0 -channel 11 -dir /var/lib/airtime \
//	     -cars <hu-bssid>[,<hu-bssid>...] -box <mac>[,<mac>...]
//
// It sits in the car in monitor mode and, every second, converts every frame it
// saw into the microseconds that frame held the medium -- per transmitter, and
// in total. Bytes are reported too, but airtime is the answer: at 1x1 HT20 a
// frame at MCS 0 costs about ten times the airtime of the same frame at MCS 7,
// so a station moving 20 kB/s slowly can cost more channel than one moving
// 100 kB/s quickly.
//
// Written for a drive with no network and battery power:
//   - one append-only JSONL per run, fsynced every second, so a power cut
//     loses at most the current second instead of the file
//   - no pcap ring: this is analysis, not forensics, and 3.6 GB of pcap is
//     exactly what we do not want on battery
//   - bounded by -max-mb, oldest-first, so it cannot fill the card
//   - it never needs the network, a console, or anything to be plugged in
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// The AR9271 is documented to stop receiving in monitor mode after anywhere
// from seconds to hours, sooner on busy channels, while the interface stays up
// and tcpdump keeps running happily with nothing to read
// (qca/open-ath9k-htc-firmware#126). carrecon has a watchdog for exactly this;
// this tool had none, which on a drive means a silently empty recording and no
// way to know until you plug the card back in.
//
// Channel 11 here carries ~100 frames/s from the neighbourhood alone -- six
// APs were visible at -83 to -91 dBm while setting this up -- so a silence
// this long is a stall, never a quiet channel.
//
// Recovery is to exit non-zero: systemd restarts us, and startup puts the
// interface down, back up and on channel again, which is the first escalation
// carrecon uses. The run continues in a new file, so the gap is visible rather
// than papered over.
const (
	stallAfter = 30 * time.Second
	stallCheck = 5 * time.Second
	exitStall  = 3
)

var lastFrame atomic.Int64

type stat struct {
	Frames    int     `json:"frames"`
	Retries   int     `json:"retries"`
	Bytes     int     `json:"bytes"`
	AirUS     float64 `json:"air_us"`
	Unaccount int     `json:"unaccounted"` // frames whose rate radiotap did not give
	SigSum    int     `json:"-"`
	SigN      int     `json:"-"`
	Sig       int     `json:"sig"`
	MaxMbps   float64 `json:"max_mbps"`
	MinMbps   float64 `json:"min_mbps"`
	Role      string  `json:"role"`
	ToHU      int     `json:"to_hu_bytes"`
	AirToHU   float64 `json:"to_hu_air_us"`
}

type second struct {
	T         string           `json:"t"`
	Frames    int              `json:"frames"`
	Bytes     int              `json:"bytes"`
	AirUS     float64          `json:"air_us"`
	BusyPct   float64          `json:"busy_pct"`
	BadFCS    int              `json:"bad_fcs"`
	Unaccount int              `json:"unaccounted"`
	Stations  map[string]*stat `json:"stations"`
	// HeadroomMbps is what is plausibly left for us, at the best rate seen on
	// the channel this second. An estimate, and labelled as one in the README.
	HeadroomMbps float64 `json:"headroom_mbps"`
	BestMbps     float64 `json:"best_mbps"`
}

var (
	iface   = flag.String("iface", "mon0", "monitor-mode interface")
	channel = flag.Int("channel", 11, "2.4 GHz channel to sit on")
	dir     = flag.String("dir", "/var/lib/airtime", "output directory")
	carList = flag.String("cars", "", "head-unit BSSIDs, comma separated")
	boxList = flag.String("box", "", "our box's radio MACs, comma separated")
	maxMB   = flag.Int("max-mb", 512, "delete oldest runs above this")
	setup   = flag.Bool("setup", true, "put the interface into monitor mode on the channel")
	report  = flag.String("report", "", "analyse a run file and exit (no capture)")
)

func main() {
	flag.Parse()
	if *report != "" {
		if err := reportFile(*report, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "airtime:", err)
			os.Exit(1)
		}
		return
	}
	cars := macSet(*carList)
	box := macSet(*boxList)
	if len(cars) == 0 {
		fmt.Fprintln(os.Stderr, "airtime: -cars is required (the head-unit BSSIDs)")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "airtime:", err)
		os.Exit(1)
	}
	prune(*dir, int64(*maxMB)*1024*1024)

	if *setup {
		if err := monitorMode(*iface, *channel); err != nil {
			fmt.Fprintln(os.Stderr, "airtime: monitor mode:", err)
			os.Exit(1)
		}
	}

	path := filepath.Join(*dir, time.Now().Format("20060102-150405")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "airtime:", err)
		os.Exit(1)
	}
	defer f.Close()
	fmt.Println("airtime: writing", path)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// tcpdump is the capture: it handles the ring buffer and the privilege
	// drop, and writing pcap to a pipe means no file ever touches the card.
	cmd := exec.Command("tcpdump", "-i", *iface, "-n", "-U", "-s", "256", "-B", "8192", "-w", "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "airtime:", err)
		os.Exit(1)
	}
	var errb strings.Builder
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "airtime: tcpdump:", err)
		os.Exit(1)
	}
	go func() {
		<-stop
		cmd.Process.Signal(syscall.SIGTERM)
	}()

	lastFrame.Store(time.Now().UnixNano())
	go watchStall(f, cmd)

	if err := run(bufio.NewReaderSize(out, 1<<20), f, cars, box); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "airtime:", err, strings.TrimSpace(errb.String()))
	}
	cmd.Wait()
}

// run reads the pcap stream and emits one JSON object per second.
func run(r io.Reader, w *os.File, cars, box map[string]bool) error {
	var hdr [24]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("pcap header: %w", err)
	}
	bo := binary.ByteOrder(binary.LittleEndian)
	switch binary.LittleEndian.Uint32(hdr[:4]) {
	case 0xa1b2c3d4, 0xa1b23c4d:
	default:
		if be := binary.BigEndian.Uint32(hdr[:4]); be == 0xa1b2c3d4 || be == 0xa1b23c4d {
			bo = binary.BigEndian
		} else {
			return fmt.Errorf("not a pcap stream")
		}
	}
	nanos := bo.Uint32(hdr[:4]) == 0xa1b23c4d
	link := int(bo.Uint32(hdr[20:24]))
	if link != 127 && link != 105 {
		return fmt.Errorf("link type %d is not 802.11; is %s in monitor mode?", link, *iface)
	}

	cur := newSecond()
	var curT int64
	var ph [16]byte
	buf := make([]byte, 65536)
	enc := json.NewEncoder(w)

	for {
		if _, err := io.ReadFull(r, ph[:]); err != nil {
			flush(enc, w, cur, curT, cars, box)
			return err
		}
		sec := int64(bo.Uint32(ph[0:4]))
		frac := int64(bo.Uint32(ph[4:8]))
		if !nanos {
			frac *= 1000
		}
		inclLen := int(bo.Uint32(ph[8:12]))
		origLen := int(bo.Uint32(ph[12:16]))
		if inclLen < 0 || inclLen > len(buf) {
			return fmt.Errorf("absurd packet length %d", inclLen)
		}
		if _, err := io.ReadFull(r, buf[:inclLen]); err != nil {
			flush(enc, w, cur, curT, cars, box)
			return err
		}
		_ = frac

		if curT == 0 {
			curT = sec
		}
		if sec != curT {
			flush(enc, w, cur, curT, cars, box)
			cur, curT = newSecond(), sec
		}
		lastFrame.Store(time.Now().UnixNano())
		account(cur, buf[:inclLen], origLen, link, cars, box)
	}
}

func newSecond() *second { return &second{Stations: map[string]*stat{}} }

// account charges one frame to its transmitter and to the channel total.
func account(s *second, pkt []byte, origLen, link int, cars, box map[string]bool) {
	var rt radiotap
	w := pkt
	if link == 127 {
		rt = parseRadiotap(pkt)
		if !rt.OK || rt.HeaderLen > len(pkt) {
			return
		}
		w = pkt[rt.HeaderLen:]
	}
	if rt.BadFCS {
		s.BadFCS++
		return
	}
	f, ok := parseFrame(w)
	if !ok {
		return
	}
	// The on-air length, not the captured length: -s 256 truncates the copy
	// but the frame still held the medium for its full size.
	n := origLen
	if link == 127 {
		n -= rt.HeaderLen
	}
	if n <= 0 {
		return
	}

	us := frameCostUS(n, rt.Rate, f.Unicast)
	s.Frames++
	s.Bytes += n
	if us == 0 {
		s.Unaccount++
	} else {
		s.AirUS += us
	}
	if mb := rt.Rate.Mbps(); mb > s.BestMbps {
		s.BestMbps = mb
	}

	tx := mac(f.Addr2)
	if tx == "" {
		return
	}
	st := s.Stations[tx]
	if st == nil {
		st = &stat{MinMbps: 1e9}
		s.Stations[tx] = st
	}
	st.Frames++
	st.Bytes += n
	if us == 0 {
		st.Unaccount++
	} else {
		st.AirUS += us
	}
	if f.Retry {
		st.Retries++
	}
	if rt.Signal != 0 {
		st.SigSum += rt.Signal
		st.SigN++
	}
	if mb := rt.Rate.Mbps(); mb > 0 {
		if mb > st.MaxMbps {
			st.MaxMbps = mb
		}
		if mb < st.MinMbps {
			st.MinMbps = mb
		}
	}
	// Traffic aimed at a head unit is CarPlay's own direction.
	if cars[mac(f.Addr1)] {
		st.ToHU += n
		st.AirToHU += us
	}
}

func flush(enc *json.Encoder, w *os.File, s *second, t int64, cars, box map[string]bool) {
	if t == 0 || s.Frames == 0 {
		return
	}
	s.T = time.Unix(t, 0).UTC().Format(time.RFC3339)
	s.BusyPct = s.AirUS / 1e6 * 100
	if s.BusyPct > 100 {
		s.BusyPct = 100
	}
	// Headroom: the idle fraction of the second, at the best rate anyone
	// achieved on this channel. An estimate -- contention and the fact that
	// our traffic is relayed (so it crosses the air twice) make the real
	// figure lower, which the README states.
	s.HeadroomMbps = s.BestMbps * (100 - s.BusyPct) / 100
	for m, st := range s.Stations {
		if st.SigN > 0 {
			st.Sig = st.SigSum / st.SigN
		}
		if st.MinMbps > 1e8 {
			st.MinMbps = 0
		}
		st.Role = role(m, st, cars, box)
	}
	_ = enc.Encode(s)
	// Battery power: a cut must lose the current second, not the file.
	_ = w.Sync()
}

// role is decided from what a station DID over the whole second, not from
// whichever frame happened to arrive first.
//
// The first version classified on first sight, and it filed the single biggest
// airtime consumer on the channel as a stranger: the iPhone at -36 dBm pushing
// 650 kB/s, every byte of it addressed to a head unit, came out as "other"
// because its first captured frame did not carry a usable BSSID. The report
// then said CarPlay used 11.9% of the airtime when the real figure was about
// five times that. A classifier that can be wrong about the loudest station on
// the channel is worse than none, because it looks authoritative.
//
// So: a station is a phone if the clear majority of its bytes went to a known
// head unit. That is behaviour, and it cannot be fooled by frame ordering.
func role(tx string, st *stat, cars, box map[string]bool) string {
	switch {
	case cars[tx]:
		return "hu"
	case box[tx]:
		return "box"
	}
	if st.Bytes > 0 && float64(st.ToHU)/float64(st.Bytes) > 0.5 {
		return "phone"
	}
	return "other"
}

func mac(b []byte) string {
	if len(b) != 6 {
		return ""
	}
	const h = "0123456789abcdef"
	out := make([]byte, 0, 17)
	for i, c := range b {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, h[c>>4], h[c&0xf])
	}
	return string(out)
}

func macSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			m[p] = true
		}
	}
	return m
}

func monitorMode(dev string, ch int) error {
	for _, args := range [][]string{
		{"ip", "link", "set", dev, "down"},
		{"iw", "dev", dev, "set", "type", "monitor"},
		{"ip", "link", "set", dev, "up"},
		{"iw", "dev", dev, "set", "channel", fmt.Sprint(ch)},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// prune keeps the directory under a byte budget, oldest first. On battery, in
// a car, with nobody watching, filling the card is the one failure that loses
// every previous run too.
func prune(d string, max int64) {
	ents, err := os.ReadDir(d)
	if err != nil {
		return
	}
	type fi struct {
		path string
		mod  time.Time
		size int64
	}
	var all []fi
	var total int64
	for _, e := range ents {
		inf, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		all = append(all, fi{filepath.Join(d, e.Name()), inf.ModTime(), inf.Size()})
		total += inf.Size()
	}
	for i := 0; i < len(all) && total > max; i++ {
		oldest := 0
		for j := range all {
			if all[j].size >= 0 && all[j].mod.Before(all[oldest].mod) {
				oldest = j
			}
		}
		if os.Remove(all[oldest].path) == nil {
			total -= all[oldest].size
		}
		all[oldest].mod = time.Now().Add(time.Hour)
	}
}

// watchStall exits the process when frames stop arriving, so systemd restarts
// it and the interface is rebuilt. It writes one line into the run file first,
// so the gap is explained to whoever reads the capture rather than looking
// like the car was simply switched off.
func watchStall(w *os.File, cmd *exec.Cmd) {
	t := time.NewTicker(stallCheck)
	defer t.Stop()
	for range t.C {
		idle := time.Since(time.Unix(0, lastFrame.Load()))
		if idle < stallAfter {
			continue
		}
		rec := map[string]any{
			"t":      time.Now().UTC().Format(time.RFC3339),
			"event":  "stall",
			"idle_s": int(idle.Seconds()),
			"note": "no frames on a channel that always has neighbours: the adapter " +
				"has gone deaf. Exiting so systemd rebuilds the interface; the run " +
				"continues in a new file.",
		}
		if b, err := json.Marshal(rec); err == nil {
			w.Write(append(b, '\n'))
			w.Sync()
		}
		fmt.Fprintf(os.Stderr, "airtime: stalled for %s, exiting to be restarted\n", idle.Round(time.Second))
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
		}
		os.Exit(exitStall)
	}
}
