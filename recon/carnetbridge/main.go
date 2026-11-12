// carnetbridge joins the in-car recorder to the CarNet Beam over USB.
//
// The Beam has a GPS; the recorder's Pi, in the car, has no network and so no
// NTP. Rather than step the Pi's clock mid-capture -- which would corrupt the
// very timestamps the whole analysis merges on -- the bridge writes the Beam's
// GPS time next to the Pi's own clock once a second, with the difference. The
// two logs can then be aligned exactly, afterwards, with no risk during the
// drive.
//
// It also forwards marks the instant they happen, so the moment the driver
// taps "CarPlay just dropped" lands in the recorder's own timeline instead of
// being reconstructed later.
//
//	carnetbridge [-dev /dev/ttyACM0] [-out /var/lib/carrecon/beam.events]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const stamp = "2006-01-02T15:04:05.000Z07:00"

type bridge struct {
	out      *os.File
	lines    uint64
	marks    uint64
	lastSkew time.Duration
	haveSkew bool
	skews    []int64   // recent skews, ms, for the periodic summary
	nextSum  time.Time
}

// summarise reports the median skew over the recent window.
//
// The raw skew is the Pi's clock minus the Beam's GPS time, so it contains a
// fixed pipeline latency (the Beam reports a GPS second about a second after it
// began) plus whatever the Pi's clock is actually off by. Measured against an
// NTP-locked Pi the latency alone came to ~1045 ms, very stable. Logging the
// median before the drive and again during it therefore separates the two: any
// movement away from that baseline is the Pi's clock drifting once NTP is gone,
// which is the number the log merge actually needs.
//
// Median, not mean: a row built twice inside one GPS second shows up as a
// ~1000 ms outlier and would drag an average.
func (b *bridge) summarise() {
	if len(b.skews) == 0 {
		return
	}
	v := append([]int64(nil), b.skews...)
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
	med := v[len(v)/2]
	b.ev("skew_summary", fmt.Sprintf("median_ms=%d n=%d min_ms=%d max_ms=%d", med, len(v), v[0], v[len(v)-1]))
	b.skews = b.skews[:0]
}

func (b *bridge) ev(name string, args ...string) {
	l := fmt.Sprintf("%s %s %s", time.Now().Format(stamp), name, strings.Join(args, " "))
	l = strings.TrimSpace(l)
	fmt.Println(l)
	if b.out != nil {
		b.out.WriteString(l + "\n")
	}
}

// findDev picks the Beam's USB-CDC device. It is the only ACM device in the
// car, so the first one is it; naming it explicitly is still supported.
func findDev(want string) (string, error) {
	if want != "" {
		if _, err := os.Stat(want); err != nil {
			return "", err
		}
		return want, nil
	}
	for _, pat := range []string{"/dev/ttyACM*", "/dev/ttyUSB*"} {
		m, _ := filepath.Glob(pat)
		if len(m) > 0 {
			return m[0], nil
		}
	}
	return "", fmt.Errorf("no ttyACM/ttyUSB device")
}

// parseGPSTime reads the Beam's time field. Before a position fix it still
// carries real UTC, which is the point: clock alignment does not need a fix.
// Before the GPS has said anything at all the Beam sends "boot+<n>s", which is
// deliberately not a timestamp and must not be treated as one.
func parseGPSTime(f string) (time.Time, bool) {
	if strings.HasPrefix(f, "boot+") {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, f); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// skew is how far the Pi's clock is from the Beam's GPS time. Positive means
// the Pi is ahead.
func skew(now, gps time.Time) time.Duration { return now.Sub(gps) }

func (b *bridge) handle(line string) {
	line = strings.TrimSpace(line)
	f := strings.Split(line, ",")
	switch {
	case strings.HasPrefix(line, "$CNBMARK") && len(f) >= 6:
		b.marks++
		args := []string{"mark=" + f[2], "gps_time=" + f[1], "mph=" + f[3],
			"lat=" + f[4], "lon=" + f[5]}
		if len(f) >= 7 {
			args = append(args, "heading="+f[6])
		}
		if gt, ok := parseGPSTime(f[1]); ok {
			args = append(args, fmt.Sprintf("pi_skew_ms=%d", skew(time.Now(), gt).Milliseconds()))
		}
		b.ev("beam_mark", args...)

	case strings.HasPrefix(line, "$CNB") && len(f) >= 13:
		b.lines++
		args := []string{"gps_time=" + f[1], "fix=" + f[2], "sats=" + f[3],
			"lat=" + f[4], "lon=" + f[5], "mph=" + f[6], "heading=" + f[7],
			"hdop=" + f[8], "accel=" + f[9], "turn=" + f[10],
			"marks=" + f[11], "batt_mv=" + f[12]}
		if gt, ok := parseGPSTime(f[1]); ok {
			d := skew(time.Now(), gt)
			b.lastSkew, b.haveSkew = d, true
			b.skews = append(b.skews, d.Milliseconds())
			args = append(args, fmt.Sprintf("pi_skew_ms=%d", d.Milliseconds()))
		} else {
			args = append(args, "pi_skew_ms=unknown")
		}
		b.ev("beam", args...)
		if now := time.Now(); now.After(b.nextSum) {
			b.summarise()
			b.nextSum = now.Add(time.Minute)
		}
	}
}

func main() {
	dev := flag.String("dev", "", "serial device (default: the first ttyACM/ttyUSB)")
	out := flag.String("out", "/var/lib/carrecon/beam.events", "event log to append to")
	baud := flag.String("baud", "115200", "line speed for stty")
	flag.Parse()

	b := &bridge{}
	if *out != "" {
		os.MkdirAll(filepath.Dir(*out), 0o755)
		f, err := os.OpenFile(*out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "carnetbridge:", err)
			os.Exit(1)
		}
		b.out = f
	}
	b.ev("start", "out="+*out)

	lastErr := ""
	for {
		d, err := findDev(*dev)
		if err != nil {
			if err.Error() != lastErr { // once per change, not every two seconds
				b.ev("waiting", err.Error())
				lastErr = err.Error()
			}
			time.Sleep(2 * time.Second)
			continue
		}
		// Raw mode, or the line discipline will eat and reorder things.
		exec.Command("stty", "-F", d, "raw", "-echo", *baud).Run()
		fh, err := os.Open(d)
		if err != nil {
			b.ev("error", "open "+d, err.Error())
			time.Sleep(2 * time.Second)
			continue
		}
		b.ev("attached", "dev="+d)
		lastErr = ""
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 4096), 8192)
		for sc.Scan() {
			b.handle(sc.Text())
		}
		fh.Close()
		// A detached Beam is normal: it is powered by this Pi, so it comes and
		// goes with the cable. Say so once and keep waiting.
		b.ev("detached", "dev="+d, fmt.Sprintf("lines=%d marks=%d", b.lines, b.marks))
		time.Sleep(2 * time.Second)
	}
}

var _ = strconv.Itoa
