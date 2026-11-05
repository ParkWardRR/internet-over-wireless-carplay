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
	"syscall"
	"time"
)

type recon struct {
	dir    string
	cars   map[string]bool // head-unit BSSIDs
	box    string          // the garage box's STA MAC
	mu     sync.Mutex
	ev     *os.File
	sec    time.Time
	stats  map[string]*stat // per transmitter in a car BSS, this second
	stream map[string]bool  // phone→HU streams seen flowing (key "phone>hu")
	quiet  map[string]int   // seconds without stream bytes
	marks  []time.Time
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
		r.event("eapol", "from="+f.a2+"("+r.role(f.a2)+")", "to="+f.a1+"("+r.role(f.a1)+")")
	}
	// Every frame in the BSS (beacons included, 10/s) closes the previous
	// second, so a stream stop is noticed on time even when the link is quiet.
	if !t.Truncate(time.Second).Equal(r.sec) {
		r.flush(r.sec)
		r.sec = t.Truncate(time.Second)
	}
	if f.ftype != 2 && !(f.ftype == 0 && f.subtype != 8) { // beacons and control frames: not in the stats
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

// streamMin: a CarPlay session moves far more than this phone → head unit
// (video + audio + control); below it for 3 s means the stream stopped.
const streamMin = 20000 // bytes/s

// flush writes one line per transmitter for the second that just ended and
// tracks the phone → head unit stream.
func (r *recon) flush(sec time.Time) {
	if sec.IsZero() {
		r.stats = map[string]*stat{}
		return
	}
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
			if r.bytesTo(s, "hu") >= streamMin {
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
		if s := r.stats[phone]; s != nil && r.bytesTo(s, "hu") >= streamMin {
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

func main() {
	carList := flag.String("car", "", "head-unit BSSIDs, comma-separated (from secrets)")
	box := flag.String("box", "", "the garage box's Wi-Fi MAC")
	channel := flag.Int("channel", 11, "2.4 GHz channel of the cars")
	dir := flag.String("dir", "/var/lib/carrecon", "output directory")
	driver := flag.String("driver", "ath9k_htc", "monitor adapter's driver")
	iface := flag.String("iface", "", "use this monitor interface as is (testing); skips adapter setup")
	maxGB := flag.Float64("max-gb", 8, "pcap ring size")
	rotate := flag.Duration("rotate", time.Minute, "pcap file length")
	flag.Parse()

	r := &recon{dir: *dir, cars: map[string]bool{}, box: strings.ToLower(*box), stats: map[string]*stat{}, stream: map[string]bool{}, quiet: map[string]int{}}
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
		// One tcpdump writes the ring, another streams to the parser.
		ring := exec.Command("tcpdump", "-i", dev, "-n", "-q", "-U", "-G", fmt.Sprint(int(rotate.Seconds())),
			"-w", filepath.Join(capDir, "%Y%m%d-%H%M%S.pcap"))
		live := exec.Command("tcpdump", "-i", dev, "-n", "-q", "-U", "-s", "256", "-w", "-")
		out, _ := live.StdoutPipe()
		if err := ring.Start(); err != nil {
			r.event("error", "tcpdump ring", err.Error())
		}
		if err := live.Start(); err != nil {
			r.event("error", "tcpdump live", err.Error())
		}
		done := make(chan error, 1)
		go func() { done <- r.readPcap(out) }()
		select {
		case <-stop:
			ring.Process.Signal(syscall.SIGTERM)
			live.Process.Signal(syscall.SIGTERM)
			ring.Wait()
			live.Wait()
			r.flush(r.sec)
			r.event("stop")
			return
		case err := <-done:
			r.event("capture_lost", fmt.Sprint(err))
			ring.Process.Signal(syscall.SIGTERM)
			live.Process.Kill()
			ring.Wait()
			live.Wait()
			time.Sleep(2 * time.Second)
		}
	}
}
