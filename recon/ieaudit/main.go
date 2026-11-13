// ieaudit decodes and validates the beacon elements this project's AP-signalling
// experiment turns on and off: the 802.11u Interworking element (ID 107) and the
// Apple Device IE (vendor 00:A0:40 type 0).
//
//	ieaudit hex   dd0800a04000000280226b0110    decode a raw element blob
//	ieaudit pcap  capture.pcap                  decode every beacon/probe response
//	ieaudit check capture.pcap --bssid aa:.. --internet 1
//	                                            gate: assert the treatment reached the air
//
// It exists because the trial matrix in docs/ap-signalling.md compares an AP
// configured one way against the same AP configured another way, and a
// configuration file is not evidence of what a radio transmitted. `check` is the
// gate: it fails unless at least one beacon *and* one probe response from the
// BSSID under test carry an Interworking element with the expected Internet bit,
// and every such frame agrees.
//
// Output masks identity-bearing material by default — SSID digit runs (the end
// of the VIN on these cars), the low three octets of every MAC, and the Apple IE
// sub-elements that carry device identifiers — so a run can be pasted into the
// results table in docs/ap-signalling.md. --unmask prints it raw for local use;
// keep that output in captures/, which is not tracked.
package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

var unmask bool

// gateFail is a trial that ran and did not pass, as opposed to a tool or
// capture problem. It exits 1; everything else exits 2, so a test script can
// tell "the treatment did not reach the air" from "the capture was unreadable".
type gateFail struct{ msg string }

func (e gateFail) Error() string { return e.msg }

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	var g gateFail
	if errors.As(err, &g) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "ieaudit: "+err.Error())
	os.Exit(2)
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ieaudit hex <blob> | pcap <file> | check <file> [--bssid X] [--ssid S] --internet 0|1")
	}
	cmd, rest := args[0], args[1:]
	var positional []string
	opts := map[string]string{}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--unmask":
			unmask = true
		case strings.HasPrefix(a, "--"):
			name := strings.TrimPrefix(a, "--")
			if k, v, ok := strings.Cut(name, "="); ok {
				opts[k] = v
				continue
			}
			if i+1 >= len(rest) {
				return fmt.Errorf("--%s needs a value", name)
			}
			i++
			opts[name] = rest[i]
		default:
			positional = append(positional, a)
		}
	}
	switch cmd {
	case "hex":
		return cmdHex(positional)
	case "pcap":
		return cmdPcap(positional, opts, os.Stdout)
	case "check":
		return cmdCheck(positional, opts, os.Stdout)
	case "selftest":
		return cmdHex([]string{cannedBlob})
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// cannedBlob is an Apple Device IE with AirPlay + CarPlay + 2.4 GHz set,
// followed by an Interworking element with the Internet bit set and access
// network type 0. It matches the self-test in scripts/car-ie.swift so the Go and
// Swift decoders can be compared against the same bytes.
const cannedBlob = "dd0800a04000000280226b0110"

func cmdHex(pos []string) error {
	var src string
	if len(pos) > 0 {
		src = strings.Join(pos, "")
	} else {
		b, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return err
		}
		src = string(b)
	}
	blob, err := decodeHex(src)
	if err != nil {
		return err
	}
	lines, perr := describeElements(blob)
	for _, l := range lines {
		fmt.Println(l)
	}
	if perr != nil {
		return perr
	}
	if len(lines) == 0 {
		return errors.New("no elements in input")
	}
	return nil
}

var notHex = regexp.MustCompile(`[^0-9a-fA-F]`)

func decodeHex(s string) ([]byte, error) {
	s = notHex.ReplaceAllString(s, "")
	if s == "" {
		return nil, errors.New("no hex digits in input")
	}
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd number of hex digits (%d)", len(s))
	}
	return hex.DecodeString(s)
}

// describeElements renders the elements this tool knows about. The returned
// error is the first malformation; lines parsed before it are still returned.
func describeElements(blob []byte) ([]string, error) {
	els, err := parseElements(blob)
	var out []string
	for _, e := range els {
		switch {
		case e.ID == interworkingElemID:
			w, werr := parseInterworking(e.Body)
			if werr != nil {
				out = append(out, "interworking: "+werr.Error())
				continue
			}
			out = append(out, w.String())
		case e.ID == 221:
			oui, vtype, body, verr := vendorOUI(e)
			if verr != nil {
				out = append(out, verr.Error())
				continue
			}
			if oui != appleOUI || vtype != appleVendorType {
				out = append(out, fmt.Sprintf("vendor oui=%s type=%d len=%d (not the Apple Device IE)", hexs(oui[:]), vtype, len(body)))
				continue
			}
			a, aerr := parseAppleIE(body)
			if aerr != nil {
				out = append(out, "apple-device-ie: "+aerr.Error())
				continue
			}
			out = append(out, "apple-device-ie: "+a.String())
		case e.ID == 0:
			out = append(out, "ssid="+maskSSID(printable(e.Body)))
		}
	}
	return out, err
}

type bssSummary struct {
	BSSID     string
	SSID      string
	Beacons   int
	Probes    int
	IWSeen    map[string]int // rendered interworking -> count
	Apple     map[string]int
	NoIW      map[int]int // subtype -> frames with no Interworking element
	Malformed []string
	Signal    int
}

func collect(path string, wantBSSID, wantSSID string) (map[string]*bssSummary, int, error) {
	p, closeFn, err := openPcap(path)
	if err != nil {
		return nil, 0, err
	}
	defer closeFn()
	out := map[string]*bssSummary{}
	frames := 0
	for {
		pk, err := p.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, frames, err
		}
		subtype, bssid, elems, ok := mgmtBody(pk.Data)
		if !ok {
			continue
		}
		raw := macString(bssid)
		if wantBSSID != "" && !strings.EqualFold(raw, wantBSSID) {
			continue
		}
		els, perr := parseElements(elems)
		ssid := ""
		for _, e := range els {
			if e.ID == 0 {
				ssid = printable(e.Body)
				break
			}
		}
		// --ssid is a convenience; --bssid is the robust filter. A beacon
		// whose elements are malformed may have no readable SSID, so an
		// SSID filter silently drops exactly the frames worth seeing.
		if wantSSID != "" && ssid != wantSSID {
			continue
		}
		frames++
		s := out[raw]
		if s == nil {
			s = &bssSummary{BSSID: raw, IWSeen: map[string]int{}, Apple: map[string]int{}, NoIW: map[int]int{}}
			out[raw] = s
		}
		s.SSID, s.Signal = ssid, pk.Signal
		if subtype == 8 {
			s.Beacons++
		} else {
			s.Probes++
		}
		if perr != nil {
			s.Malformed = append(s.Malformed, fmt.Sprintf("%s: %v", subtypeName(subtype), perr))
		}
		if pk.Trunc {
			s.Malformed = append(s.Malformed, fmt.Sprintf("%s: capture truncated (%d of %d octets); raise the tcpdump snap length", subtypeName(subtype), len(pk.Data), pk.OrigLen))
		}
		sawIW := false
		for _, e := range els {
			switch {
			case e.ID == interworkingElemID:
				sawIW = true
				w, werr := parseInterworking(e.Body)
				if werr != nil {
					s.Malformed = append(s.Malformed, fmt.Sprintf("%s: %v", subtypeName(subtype), werr))
					continue
				}
				s.IWSeen[w.String()]++
			case e.ID == 221:
				oui, vtype, body, verr := vendorOUI(e)
				if verr != nil {
					s.Malformed = append(s.Malformed, fmt.Sprintf("%s: %v", subtypeName(subtype), verr))
					continue
				}
				if oui != appleOUI || vtype != appleVendorType {
					continue
				}
				a, aerr := parseAppleIE(body)
				if aerr != nil {
					s.Malformed = append(s.Malformed, fmt.Sprintf("%s: %v", subtypeName(subtype), aerr))
					continue
				}
				s.Apple[a.String()]++
			}
		}
		if !sawIW {
			s.NoIW[subtype]++
		}
	}
	return out, frames, nil
}

func cmdPcap(pos []string, opts map[string]string, w io.Writer) error {
	if len(pos) != 1 {
		return errors.New("usage: ieaudit pcap <file.pcap> [--bssid X] [--ssid S]")
	}
	sums, frames, err := collect(pos[0], opts["bssid"], opts["ssid"])
	if err != nil {
		return err
	}
	if frames == 0 {
		return errors.New("no beacons or probe responses matched; check the filter, the channel, and that the radio was in monitor mode")
	}
	for _, s := range sorted(sums) {
		fmt.Fprintf(w, "%s  ssid=%s  %d beacons, %d probe responses, last signal %d dBm\n",
			maskMAC(mustMAC(s.BSSID)), maskSSID(s.SSID), s.Beacons, s.Probes, s.Signal)
		for _, line := range sortedKeys(s.IWSeen) {
			fmt.Fprintf(w, "    %s  x%d\n", line, s.IWSeen[line])
		}
		if len(s.IWSeen) == 0 {
			fmt.Fprintln(w, "    no Interworking element")
		}
		for _, line := range sortedKeys(s.Apple) {
			fmt.Fprintf(w, "    apple-device-ie: %s  x%d\n", line, s.Apple[line])
		}
		if len(s.Apple) == 0 {
			fmt.Fprintln(w, "    no Apple Device IE")
		}
		for _, m := range dedup(s.Malformed) {
			fmt.Fprintf(w, "    MALFORMED %s\n", m)
		}
	}
	return nil
}

func cmdCheck(pos []string, opts map[string]string, w io.Writer) error {
	if len(pos) != 1 {
		return errors.New("usage: ieaudit check <file.pcap> --internet 0|1 [--bssid X] [--ssid S]")
	}
	want, ok := opts["internet"]
	if !ok {
		return errors.New("check needs --internet 0 or --internet 1: the treatment the AP was configured for")
	}
	if want != "0" && want != "1" {
		return fmt.Errorf("--internet must be 0 or 1, got %q", want)
	}
	if opts["bssid"] == "" && opts["ssid"] == "" {
		return errors.New("check needs --bssid or --ssid: a capture usually holds several APs, and the gate is about one of them")
	}
	sums, _, err := collect(pos[0], opts["bssid"], opts["ssid"])
	if err != nil {
		return err
	}
	fail := func(format string, a ...any) error {
		msg := fmt.Sprintf("FAIL internet="+want+": "+format, a...)
		fmt.Fprintln(w, msg)
		return gateFail{msg}
	}
	switch len(sums) {
	case 0:
		return fail("no beacons or probe responses matched the filter")
	case 1:
	default:
		return fail("%d BSSIDs matched; narrow the filter with --bssid", len(sums))
	}
	s := sorted(sums)[0]
	if s.Beacons == 0 {
		return fail("no beacons captured")
	}
	if s.Probes == 0 {
		return fail("no probe responses captured; send one with `iw dev <mon> scan` from another radio while the AP runs")
	}
	if len(s.Malformed) > 0 {
		for _, m := range dedup(s.Malformed) {
			fmt.Fprintf(w, "    MALFORMED %s\n", m)
		}
		return fail("malformed elements on the air")
	}
	if n := s.NoIW[8] + s.NoIW[5]; n > 0 {
		return fail("%d of %d frames carry no Interworking element at all", n, s.Beacons+s.Probes)
	}
	if len(s.IWSeen) != 1 {
		for _, line := range sortedKeys(s.IWSeen) {
			fmt.Fprintf(w, "    %s  x%d\n", line, s.IWSeen[line])
		}
		return fail("the AP transmitted %d different Interworking elements during the capture; it was reconfigured mid-trial", len(s.IWSeen))
	}
	line := sortedKeys(s.IWSeen)[0]
	if !strings.Contains(line, "internet="+want) {
		return fail("on air: %s", line)
	}
	fmt.Fprintf(w, "PASS internet=%s  %s  ssid=%s  %d beacons, %d probe responses\n    %s\n",
		want, maskMAC(mustMAC(s.BSSID)), maskSSID(s.SSID), s.Beacons, s.Probes, line)
	if len(s.Apple) > 0 {
		for _, l := range sortedKeys(s.Apple) {
			fmt.Fprintf(w, "    apple-device-ie: %s  x%d\n", l, s.Apple[l])
		}
	}
	return nil
}

func sorted(m map[string]*bssSummary) []*bssSummary {
	out := make([]*bssSummary, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BSSID < out[j].BSSID })
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func macString(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, ":")
}

func mustMAC(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, ":", ""))
	if err != nil {
		return nil
	}
	return b
}

// maskMAC keeps the OUI, which names a chipset vendor, and drops the three
// octets that name a specific radio.
func maskMAC(b []byte) string {
	if len(b) != 6 {
		return hexs(b)
	}
	if unmask {
		return macString(b)
	}
	return fmt.Sprintf("%02x:%02x:%02x:xx:xx:xx", b[0], b[1], b[2])
}

var digitRun = regexp.MustCompile(`\d{4,}`)

// maskSSID hides digit runs: on these cars the SSID is BMW<last 5 of the VIN>.
func maskSSID(s string) string {
	if s == "" {
		return "<hidden or none>"
	}
	if unmask {
		return s
	}
	return digitRun.ReplaceAllStringFunc(s, func(m string) string { return strings.Repeat("#", len(m)) })
}
