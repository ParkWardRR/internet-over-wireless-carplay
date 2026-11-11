// carrecon records a car's CarPlay Wi-Fi from inside the car, passively: a
// monitor-mode adapter (AR9271) on the car's channel, a rotating pcap ring,
// and a live parse of the frame stream that logs what matters for the
// "CarPlay drops on departure" investigation (docs/recon-box.md):
//
//   - deauth/disassoc/assoc frames in the car's BSS, with reason codes;
//   - WPA handshakes (EAPOL), needed later to decrypt the capture;
//   - every second: frames, bytes, retries, signal per station in the BSS;
//   - the phone ↔ head unit stream stopping (CarPlay video/audio gone).
//
// Interesting moments mark the pcap files around them as kept, so the ring
// never overwrites a departure.
//
//	carrecon -car bc:30:7e:xx:xx:xx[,8c:57:9b:xx:xx:xx] -box xx:xx:… [-channel 11] [-dir /var/lib/carrecon]
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type recon struct {
	dir     string
	cars    map[string]bool // head-unit BSSIDs
	box     string          // the garage box's STA MAC
	mu      sync.Mutex
	ev      *os.File
	sec     time.Time
	stats   map[string]*stat // per transmitter in a car BSS, this second
	beacons map[string]*stat // per car BSSID: beacons this second (RSSI track)
	stream  map[string]bool  // phone→HU streams seen flowing (key "phone>hu")
	quiet   map[string]int   // seconds without stream bytes
	marks   []time.Time

	streamMin int // bytes/s phone -> head unit that counts as a live CarPlay stream

	// Capture liveness, read by the stall watchdog. Atomic, not under mu: the
	// hot path touches them once per frame.
	frames    atomic.Uint64
	lastFrame atomic.Int64 // UnixNano of the last frame the parser saw
	stalls    atomic.Int64 // consecutive stalls without a recovery
}

type stat struct {
	frames, bytes, retries, sigSum, sigN int
	to                                   map[string]int // bytes per receiver
}

func (r *recon) event(name string, args ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := fmt.Sprintf("%s %s %s", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), name, strings.Join(args, " "))
	l = strings.TrimSpace(l)
	fmt.Println(l)
	if r.ev != nil {
		r.ev.WriteString(l + "\n")
	}
}

func (r *recon) mark(why string) {
	r.mu.Lock()
	r.marks = append(r.marks, time.Now())
	r.mu.Unlock()
	r.event("mark", why)
}

func (r *recon) role(m string) string {
	switch {
	case r.cars[m]:
		return "hu"
	case m == r.box:
		return "box"
	}
	return "sta"
}

// handle processes one frame.
func (r *recon) handle(t time.Time, f frame) {
	bss := f.bssid()
	if !r.cars[bss] {
		return
	}
	switch {
	case f.ftype == 0 && (f.subtype == 10 || f.subtype == 12):
		kind := map[int]string{10: "disassoc", 12: "deauth"}[f.subtype]
		r.event(kind, "from="+f.a2+"("+r.role(f.a2)+")", "to="+f.a1+"("+r.role(f.a1)+")", fmt.Sprintf("reason=%d", f.reason), fmt.Sprintf("sig=%d", f.signal))
		if r.role(f.a1) != "box" && r.role(f.a2) != "box" {
			r.mark(kind) // the phone (or anyone but the box) leaving the car's Wi-Fi
		}
	case f.ftype == 0 && (f.subtype == 0 || f.subtype == 2):
		r.event("assoc_req", "sta="+f.a2+"("+r.role(f.a2)+")", fmt.Sprintf("reassoc=%t", f.subtype == 2), fmt.Sprintf("sig=%d", f.signal))
	case f.ftype == 0 && (f.subtype == 1 || f.subtype == 3):
		r.event("assoc_resp", "sta="+f.a1+"("+r.role(f.a1)+")")
	case f.eapol:
		// m=1..4 identifies the 4-way handshake message. All four for a
		// station mean its traffic can be decrypted offline; a gap means it
		// cannot, and that is worth knowing before a drive, not after.
		r.event("eapol", "from="+f.a2+"("+r.role(f.a2)+")", "to="+f.a1+"("+r.role(f.a1)+")",
			fmt.Sprintf("m=%d", f.eapolMsg))
	}
	// Every frame in the BSS (beacons included, 10/s) closes the previous
	// second, so a stream stop is noticed on time even when the link is quiet.
	if !t.Truncate(time.Second).Equal(r.sec) {
		r.flush(r.sec)
		r.sec = t.Truncate(time.Second)
	}
	// Beacons: an RSSI track of the head unit itself, 10/s, independent of any
	// traffic. On a departure drive this is the only signal a car gives once
	// its link goes idle, so it is what plots range against where the car is.
	if f.ftype == 0 && f.subtype == 8 {
		if r.beacons == nil {
			r.beacons = map[string]*stat{}
		}
		s := r.beacons[bss]
		if s == nil {
			s = &stat{to: map[string]int{}}
			r.beacons[bss] = s
		}
		s.frames++
		if f.retry {
			s.retries++
		}
		if f.signal != 0 {
			s.sigSum += f.signal
			s.sigN++
		}
		return
	}
	if f.ftype != 2 && !(f.ftype == 0 && f.subtype != 8) { // control frames: not in the stats
		return
	}
	s := r.stats[f.a2]
	if s == nil {
		s = &stat{to: map[string]int{}}
		r.stats[f.a2] = s
	}
	s.frames++
	s.bytes += f.length
	s.to[f.a1] += f.length
	if f.retry {
		s.retries++
	}
	if f.signal != 0 {
		s.sigSum += f.signal
		s.sigN++
	}
}

// defaultStreamMin: bytes/s phone → head unit that count as a live CarPlay
// session; below it for 3 s means the stream stopped.
//
// Deliberately low. A CarPlay session mirroring a moving map runs into the
// hundreds of KB/s, but a static screen with only audio compresses to far
// less, and a threshold set for the busy case reports a drop that never
// happened. A session that has actually died goes to ~0, so the floor only
// has to clear silence. The per-second byte counts are in the `sec` events
// either way, so any threshold can be re-derived offline.
const defaultStreamMin = 4000 // bytes/s

// flush writes one line per transmitter for the second that just ended and
// tracks the phone → head unit stream.
func (r *recon) flush(sec time.Time) {
	if sec.IsZero() {
		r.stats, r.beacons = map[string]*stat{}, map[string]*stat{}
		return
	}
	bkeys := make([]string, 0, len(r.beacons))
	for k := range r.beacons {
		bkeys = append(bkeys, k)
	}
	sort.Strings(bkeys)
	for _, k := range bkeys {
		s := r.beacons[k]
		sig := 0
		if s.sigN > 0 {
			sig = s.sigSum / s.sigN
		}
		r.event("beacon", "hu="+k, fmt.Sprintf("count=%d sig=%d retries=%d", s.frames, sig, s.retries))
	}
	r.beacons = map[string]*stat{}
	keys := make([]string, 0, len(r.stats))
	for k := range r.stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := r.stats[k]
		sig := 0
		if s.sigN > 0 {
			sig = s.sigSum / s.sigN
		}
		r.event("sec", "tx="+k+"("+r.role(k)+")", fmt.Sprintf("frames=%d bytes=%d retries=%d sig=%d", s.frames, s.bytes, s.retries, sig), fmt.Sprintf("to_hu=%d to_box=%d", r.bytesTo(s, "hu"), r.bytesTo(s, "box")))
		if r.role(k) == "sta" {
			key := k + ">hu"
			if r.bytesTo(s, "hu") >= r.minStream() {
				if !r.stream[key] {
					r.event("stream_start", "phone="+k)
				}
				r.stream[key], r.quiet[key] = true, 0
			}
		}
	}
	for key, on := range r.stream {
		if !on {
			continue
		}
		phone := strings.TrimSuffix(key, ">hu")
		if s := r.stats[phone]; s != nil && r.bytesTo(s, "hu") >= r.minStream() {
			continue
		}
		if r.quiet[key]++; r.quiet[key] == 3 {
			r.stream[key] = false
			r.event("stream_stop", "phone="+phone)
			r.mark("stream_stop")
		}
	}
	r.stats = map[string]*stat{}
}

// minStream is the live-stream floor, falling back to the default so a recon
// built by hand (tests) behaves like the daemon.
func (r *recon) minStream() int {
	if r.streamMin > 0 {
		return r.streamMin
	}
	return defaultStreamMin
}

func (r *recon) bytesTo(s *stat, role string) int {
	n := 0
	for to, b := range s.to {
		if r.role(to) == role {
			n += b
		}
	}
	return n
}

// readPcap parses a pcap stream (tcpdump -w -, LINKTYPE_IEEE802_11_RADIOTAP).
func (r *recon) readPcap(in io.Reader) error {
	br := bufio.NewReaderSize(in, 1<<20)
	var gh [24]byte
	if _, err := io.ReadFull(br, gh[:]); err != nil {
		return err
	}
	var bo binary.ByteOrder = binary.LittleEndian
	nano := false
	switch binary.LittleEndian.Uint32(gh[:]) {
	case 0xa1b2c3d4:
	case 0xa1b23c4d:
		nano = true
	case 0xd4c3b2a1:
		bo = binary.BigEndian
	case 0x4d3cb2a1:
		bo, nano = binary.BigEndian, true
	default:
		return fmt.Errorf("not a pcap stream")
	}
	if lt := bo.Uint32(gh[20:]); lt != 127 {
		return fmt.Errorf("link type %d, want 127 (radiotap)", lt)
	}
	var rh [16]byte
	buf := make([]byte, 65536)
	for {
		if _, err := io.ReadFull(br, rh[:]); err != nil {
			return err
		}
		sec, frac, incl, orig := bo.Uint32(rh[0:]), bo.Uint32(rh[4:]), int(bo.Uint32(rh[8:])), int(bo.Uint32(rh[12:]))
		if incl > len(buf) {
			buf = make([]byte, incl)
		}
		if _, err := io.ReadFull(br, buf[:incl]); err != nil {
			return err
		}
		ns := int64(frac) * 1000
		if nano {
			ns = int64(frac)
		}
		if f, ok := parseFrame(buf[:incl], orig); ok {
			r.frames.Add(1)
			r.lastFrame.Store(time.Now().UnixNano())
			r.handle(time.Unix(int64(sec), ns), f)
		}
	}
}

// monitor finds the adapter (driver ath9k_htc by default), removes its
// other interfaces and creates mon0 on the channel.
func monitor(driver string, channel int) (string, error) {
	phys, _ := filepath.Glob("/sys/class/ieee80211/*")
	for _, p := range phys {
		d, err := os.Readlink(filepath.Join(p, "device", "driver"))
		if err != nil || filepath.Base(d) != driver {
			continue
		}
		phy := filepath.Base(p)
		nets, _ := filepath.Glob(filepath.Join(p, "device", "net", "*"))
		for _, n := range nets {
			if filepath.Base(n) != "mon0" {
				exec.Command("iw", "dev", filepath.Base(n), "del").Run()
			}
		}
		if _, err := os.Stat("/sys/class/net/mon0"); err != nil {
			if out, err := exec.Command("iw", "phy", phy, "interface", "add", "mon0", "type", "monitor").CombinedOutput(); err != nil {
				return "", fmt.Errorf("iw add mon0: %v: %s", err, out)
			}
		}
		if out, err := exec.Command("ip", "link", "set", "mon0", "up").CombinedOutput(); err != nil {
			return "", fmt.Errorf("mon0 up: %v: %s", err, out)
		}
		if out, err := exec.Command("iw", "dev", "mon0", "set", "channel", fmt.Sprint(channel), "HT20").CombinedOutput(); err != nil {
			return "", fmt.Errorf("set channel: %v: %s", err, out)
		}
		return phy, nil
	}
	return "", fmt.Errorf("no %s adapter", driver)
}

// prune keeps the pcap directory under maxBytes, deleting the oldest files
// first but never a file within keepAround of a mark.
func (r *recon) prune(capDir string, maxBytes int64, keepAround time.Duration) {
	files, _ := filepath.Glob(filepath.Join(capDir, "*.pcap"))
	sort.Strings(files) // names are timestamps
	var total int64
	type fi struct {
		path string
		size int64
		mod  time.Time
	}
	var all []fi
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			all = append(all, fi{f, st.Size(), st.ModTime()})
			total += st.Size()
		}
	}
	r.mu.Lock()
	marks := append([]time.Time(nil), r.marks...)
	r.mu.Unlock()
	for _, f := range all[:max(len(all)-2, 0)] { // never the file being written
		if total <= maxBytes {
			return
		}
		kept := false
		for _, m := range marks {
			// A file covers [mod - rotate, mod]; keep it if a mark is near.
			if f.mod.After(m.Add(-keepAround)) && f.mod.Before(m.Add(keepAround+time.Minute)) {
				kept = true
				break
			}
		}
		if kept {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// ring runs the pcap ring and restarts it whenever it exits, until stop.
//
// -Z root matters: tcpdump otherwise drops privileges to user "tcpdump" after
// opening the first file, and then cannot create the next rotation file in the
// 0700 output directory. The ring died one rotation in ("Permission denied")
// while the live parser, which only writes to a pipe, kept running — so the
// events log looked healthy with no capture behind it.
func (r *recon) ring(dev, capDir string, rotate time.Duration, stop <-chan struct{}) {
	for {
		// -B 16384: a 16 MB kernel capture buffer. Channel 11 here runs ~500
		// frames/s with bursts well above that, and anything that overflows
		// the default buffer is counted as "dropped by kernel" and lost.
		cmd := exec.Command("tcpdump", "-i", dev, "-n", "-q", "-U", "-Z", "root", "-B", "16384",
			"-G", fmt.Sprint(int(rotate.Seconds())),
			"-w", filepath.Join(capDir, "%Y%m%d-%H%M%S.pcap"))
		var errb strings.Builder
		cmd.Stderr = &errb
		if err := cmd.Start(); err != nil {
			r.event("error", "tcpdump ring", err.Error())
		} else {
			r.event("ring_start", "dev="+dev, fmt.Sprintf("pid=%d rotate=%s", cmd.Process.Pid, rotate))
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-stop:
				cmd.Process.Signal(syscall.SIGTERM)
				cmd.Wait() // reap
				return
			case err := <-done:
				r.event("ring_died", fmt.Sprint(err), strings.TrimSpace(errb.String()))
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// The AR9271 is documented to stop receiving in monitor mode after anywhere
// from seconds to hours, sooner on busy channels, while the interface stays up
// and tcpdump keeps running happily with nothing to read
// (qca/open-ath9k-htc-firmware#126). Channel 11 here carries ~500 frames/s from
// the neighbourhood alone, so a silence this long is a stall, never a quiet
// channel -- and a stall that goes unnoticed means a drive records nothing.
const (
	stallAfter  = 30 * time.Second
	stallCheck  = 5 * time.Second
	stallReload = 2 // consecutive stalls before reloading the driver (firmware reload)
)

// watchStall recovers the capture when frames stop arriving. It escalates:
// first drop mon0 so the capture loop rebuilds it, then reload ath9k_htc,
// which re-downloads the adapter's firmware.
func (r *recon) watchStall(iface string, kick func(), stop <-chan struct{}) {
	t := time.NewTicker(stallCheck)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		last := r.lastFrame.Load()
		if last == 0 || time.Since(time.Unix(0, last)) < stallAfter {
			continue
		}
		n := r.stalls.Add(1)
		r.event("stall", fmt.Sprintf("no_frames_for=%.0fs frames_total=%d consecutive=%d",
			time.Since(time.Unix(0, last)).Seconds(), r.frames.Load(), n))
		r.ifaceStats("mon0")
		// Pretend we just saw a frame, so the next check measures the time
		// since this recovery rather than firing again immediately.
		r.lastFrame.Store(time.Now().UnixNano())
		if iface != "" {
			// Caller supplied the interface (testing): only the capture can be
			// restarted, not the adapter.
			kick()
			continue
		}
		if n >= stallReload {
			r.event("stall_recover", "reload=ath9k_htc")
			exec.Command("iw", "dev", "mon0", "del").Run()
			if out, err := exec.Command("modprobe", "-r", "ath9k_htc").CombinedOutput(); err != nil {
				r.event("error", "modprobe -r", strings.TrimSpace(string(out)))
			}
			time.Sleep(2 * time.Second)
			if out, err := exec.Command("modprobe", "ath9k_htc").CombinedOutput(); err != nil {
				r.event("error", "modprobe", strings.TrimSpace(string(out)))
			}
			time.Sleep(3 * time.Second)
		} else {
			r.event("stall_recover", "recreate=mon0")
			exec.Command("iw", "dev", "mon0", "del").Run()
		}
		kick() // tear down the captures; the main loop re-runs monitor()
	}
}

// ifaceStats logs the kernel's own counters for the monitor interface. They are
// independent of the capture pipeline, so they separate "the adapter stopped
// receiving" from "the parser stopped reading".
func (r *recon) ifaceStats(dev string) {
	var args []string
	for _, k := range []string{"rx_packets", "rx_bytes", "rx_dropped", "rx_errors", "rx_fifo_errors"} {
		b, err := os.ReadFile(filepath.Join("/sys/class/net", dev, "statistics", k))
		if err != nil {
			continue
		}
		args = append(args, k+"="+strings.TrimSpace(string(b)))
	}
	if len(args) > 0 {
		r.event("iface", append([]string{"dev=" + dev}, args...)...)
	}
}

// health logs what a Pi in a car can lose: power and heat. The doc warns a
// Pi 4 browns out on most car USB ports, and an undervoltage reboot would
// leave the clock wrong and the capture truncated. Best effort; vcgencmd only
// exists on a Pi.
func (r *recon) health() {
	var args []string
	for _, a := range [][2]string{{"get_throttled", "throttled"}, {"measure_temp", "temp"}} {
		out, err := exec.Command("vcgencmd", a[0]).Output()
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(out))
		if _, after, ok := strings.Cut(v, "="); ok {
			v = after
		}
		args = append(args, a[1]+"="+v)
	}
	if len(args) > 0 {
		r.event("health", args...)
	}
}

// ringStatus reports what the ring has on disk, so a drive can be confirmed
// to be recording without opening the capture directory.
func (r *recon) ringStatus(capDir string) {
	files, _ := filepath.Glob(filepath.Join(capDir, "*.pcap"))
	var total int64
	newest := ""
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		total += st.Size()
		if b := filepath.Base(f); newest == "" || b > newest { // names are timestamps
			newest = b
		}
	}
	r.mu.Lock()
	marks := len(r.marks)
	r.mu.Unlock()
	r.event("ring_status", fmt.Sprintf("files=%d bytes=%d marks=%d", len(files), total, marks), "newest="+newest)
}

func main() {
	carList := flag.String("car", "", "head-unit BSSIDs, comma-separated (from secrets)")
	box := flag.String("box", "", "the garage box's Wi-Fi MAC")
	channel := flag.Int("channel", 11, "2.4 GHz channel of the cars")
	dir := flag.String("dir", "/var/lib/carrecon", "output directory")
	driver := flag.String("driver", "ath9k_htc", "monitor adapter's driver")
	iface := flag.String("iface", "", "use this monitor interface as is (testing); skips adapter setup")
	maxGB := flag.Float64("max-gb", 8, "pcap ring size")
	rotate := flag.Duration("rotate", time.Minute, "pcap file length")
	streamMin := flag.Int("stream-min", defaultStreamMin, "bytes/s phone -> head unit that count as a live CarPlay stream")
	flag.Parse()

	r := &recon{dir: *dir, cars: map[string]bool{}, box: strings.ToLower(*box), stats: map[string]*stat{}, beacons: map[string]*stat{}, stream: map[string]bool{}, quiet: map[string]int{}, streamMin: *streamMin}
	for _, c := range strings.Split(strings.ToLower(*carList), ",") {
		if c = strings.TrimSpace(c); c != "" {
			r.cars[c] = true
		}
	}
	if len(r.cars) == 0 {
		fmt.Fprintln(os.Stderr, "carrecon: -car <bssid>[,<bssid>] is required")
		os.Exit(2)
	}
	capDir := filepath.Join(*dir, "pcap")
	if err := os.MkdirAll(capDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var err error
	if r.ev, err = os.OpenFile(filepath.Join(*dir, "events.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	r.event("start", fmt.Sprintf("channel=%d", *channel), "cars="+*carList)

	go func() {
		for range time.Tick(30 * time.Second) {
			r.prune(capDir, int64(*maxGB*(1<<30)), 2*time.Minute)
			r.ringStatus(capDir)
			r.ifaceStats("mon0")
			r.health()
		}
	}()

	lastErr := ""
	for {
		dev := *iface
		if dev == "" {
			phy, err := monitor(*driver, *channel)
			if err != nil {
				if err.Error() != lastErr { // once per change, not every 5 s
					r.event("adapter_missing", err.Error())
					lastErr = err.Error()
				}
				select {
				case <-stop:
					r.event("stop")
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}
			dev, lastErr = "mon0", ""
			r.event("monitor", "phy="+phy, "dev=mon0")
		}
		// One tcpdump writes the ring (supervised, full frames, for offline
		// decryption), another streams truncated frames to the live parser.
		ringStop := make(chan struct{})
		go r.ring(dev, capDir, *rotate, ringStop)
		live := exec.Command("tcpdump", "-i", dev, "-n", "-q", "-U", "-B", "16384", "-s", "256", "-w", "-")
		var liveErr strings.Builder
		live.Stderr = &liveErr
		out, _ := live.StdoutPipe()
		if err := live.Start(); err != nil {
			r.event("error", "tcpdump live", err.Error())
		}
		done := make(chan error, 1)
		go func() { done <- r.readPcap(out) }()
		// The watchdog recovers a silently stalled adapter by killing the live
		// capture, which drops us into the capture_lost path below and re-runs
		// monitor().
		wdStop := make(chan struct{})
		r.lastFrame.Store(time.Now().UnixNano())
		go r.watchStall(*iface, func() {
			if live.Process != nil {
				live.Process.Kill()
			}
		}, wdStop)
		select {
		case <-stop:
			close(wdStop)
			close(ringStop)
			live.Process.Signal(syscall.SIGTERM)
			live.Wait()
			r.flush(r.sec)
			r.ringStatus(capDir)
			r.event("stop")
			return
		case err := <-done:
			close(wdStop)
			// tcpdump reports "N packets dropped by kernel" on exit: a capture
			// with drops is incomplete and the analysis has to know.
			r.event("capture_lost", fmt.Sprint(err), strings.Join(strings.Fields(liveErr.String()), " "))
			close(ringStop)
			live.Process.Kill()
			live.Wait()
			r.ifaceStats(dev)
			time.Sleep(2 * time.Second)
		}
	}
}
