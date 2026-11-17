package main

// Egress shaping on a car link.
//
// Measured on the F22 (2026-11-16) with an iPhone streaming to CarPlay while
// bulk-downloading through the box: unshaped, round-trip time to the phone
// through the head unit's client-to-client relay reached **2051 ms** at three
// packets per second, throughput collapsed to 1-4 kB/s, the CarPlay audio
// stuttered, and under sustained saturation the head unit dropped CarPlay
// entirely and needed a hard reset to pair again.
//
// Why the box can cause that at only a few hundred kB/s: the box and the phone
// are both stations on the head unit's AP with no TDLS, so every byte we send
// is relayed -- it crosses the air twice -- and CarPlay's own audio and screen
// traffic share that same AP, that same channel, and the phone's single radio.
// The default qdisc is pfifo_fast with a 1000-packet band per hardware queue,
// so a bulk TCP flow fills the head unit's buffer and starves the thing the car
// is actually for.
//
// With `rate = 2mbit` the same load gave a 378 ms worst case, 30-130 ms
// typically, no throughput collapse and no disconnect in 120 seconds. Peak
// throughput roughly halved, which is the trade: a slower download that does
// not take the dashboard down with it.
//
// This is deliberately HTB and not cake or fq_codel. The SBC's vendor kernel
// has CONFIG_NET_SCH_FQ_CODEL, _CAKE, _FQ and _TBF all unset and only
// NET_SCH_HTB=y, so HTB plus a short pfifo leaf is what is actually available
// (see docs/kernel-modules.md for the out-of-tree route if that changes). A
// short leaf is what bounds the latency; the rate cap is what keeps the head
// unit's own buffer empty.

import (
	"fmt"
	"strconv"
	"strings"
)

// shapeLeafPackets is the leaf queue depth. At 2 Mbit/s, 16 full-size packets
// is about 96 ms of buffer: deep enough not to throttle a burst into
// retransmits, shallow enough that CarPlay audio behind it still has a chance.
const shapeLeafPackets = 16

// ctrlLeafPackets / ctrlFloorKbit size the priority class that carries ICMPv6.
//
// WHY THIS CLASS EXISTS. Measured 2026-11-16 on both cars under load:
//
//	carplayd_error A ra_send write ip ::->ff02::1: sendmsg: no buffer space available
//
// Router advertisements were failing to send. The 16-packet bulk queue above is
// deliberately short, and under saturation it is full of somebody's download --
// so the RAs that keep the phone's default route alive could not get out. The
// cap meant to protect the link was starving the thing that makes the link
// work, and tightening the cap (which rate = auto does) makes it worse.
//
// So ICMPv6 gets its own strict-priority class with a guaranteed slice. It is
// tiny traffic: an RA every 3-4 s, neighbour discovery, and the occasional
// ping. A deeper queue than bulk costs nothing at this volume and means a burst
// of neighbour solicitations cannot drop an RA either.
const (
	ctrlLeafPackets = 64
	ctrlFloorKbit   = 128
)

// applyShaping installs an HTB rate cap on a car link's egress. rate is a tc
// rate string ("2mbit"); empty means no shaping, which is the default so that
// an unmeasured car or a different head unit is not silently throttled.
//
// It is idempotent: the root qdisc is deleted first, so a rejoin or a config
// reload replaces the cap rather than stacking another one. Errors are returned
// rather than fatal -- a box that cannot shape should still serve the car.
func applyShaping(sta, rate string, runner func(string, ...string) error) error {
	if rate == "" {
		return nil
	}
	total, err := parseRateKbit(rate)
	if err != nil {
		return fmt.Errorf("shaping %s: %w", sta, err)
	}
	// Below 2 kbit there is nothing to split: HTB rejects a zero-rate class,
	// and a cap that small is pathological anyway. Fall back to one class.
	split := total >= 2
	ctrl := total / 8
	if ctrl < ctrlFloorKbit {
		ctrl = ctrlFloorKbit
	}
	if ctrl > total-1 {
		ctrl = total - 1
	}
	bulk := total - ctrl
	if !split {
		ctrl, bulk = 0, total
	}

	// Not an error: there may be no qdisc yet.
	_ = runner("tc", "qdisc", "del", "dev", sta, "root")
	steps := [][]string{
		{"tc", "qdisc", "add", "dev", sta, "root", "handle", "1:", "htb", "default", "10"},
		// One parent so the two children can borrow from each other: control
		// traffic is bursty and tiny, and bulk should get the rest when it is
		// idle rather than being permanently short.
		{"tc", "class", "add", "dev", sta, "parent", "1:", "classid", "1:1", "htb", "rate", kbitToTC(total), "ceil", kbitToTC(total)},
		{"tc", "class", "add", "dev", sta, "parent", "1:1", "classid", "1:10", "htb", "rate", kbitToTC(bulk), "ceil", kbitToTC(total), "prio", "1"},
		{"tc", "qdisc", "add", "dev", sta, "parent", "1:10", "handle", "10:", "pfifo", "limit", fmt.Sprint(shapeLeafPackets)},
	}
	if split {
		steps = append(steps[:3],
			[]string{"tc", "class", "add", "dev", sta, "parent", "1:1", "classid", "1:20", "htb", "rate", kbitToTC(ctrl), "ceil", kbitToTC(total), "prio", "0"},
			[]string{"tc", "qdisc", "add", "dev", sta, "parent", "1:10", "handle", "10:", "pfifo", "limit", fmt.Sprint(shapeLeafPackets)},
			[]string{"tc", "qdisc", "add", "dev", sta, "parent", "1:20", "handle", "20:", "pfifo", "limit", fmt.Sprint(ctrlLeafPackets)},
			// ICMPv6 (next header 58, at offset 6 of the IPv6 header) into the
			// priority class. Router advertisements are ICMPv6, and they are
			// what keeps the phone's default route alive.
			[]string{"tc", "filter", "add", "dev", sta, "parent", "1:", "protocol", "ipv6", "prio", "1", "u32", "match", "u8", "58", "0xff", "at", "6", "flowid", "1:20"},
		)
	}
	for _, st := range steps {
		if err := runner(st[0], st[1:]...); err != nil {
			return fmt.Errorf("%s on %s: %w", strings.Join(st[1:4], " "), sta, err)
		}
	}
	return nil
}

// clearShaping removes the cap. Best effort: if it fails the qdisc is replaced
// on the next join anyway, and leaving a stale cap on an idle radio is harmless.
func clearShaping(sta, rate string, runner func(string, ...string) error) {
	if rate == "" {
		return
	}
	_ = runner("tc", "qdisc", "del", "dev", sta, "root")
}

// normalizeRate validates a tc rate string at config-parse time rather than
// letting a typo surface as a tc failure on the next car join, in a garage,
// with no console. tc accepts bit/kbit/mbit/gbit (and the byte-per-second
// forms); this allows the bit-rate forms only, because a cap meant as "2mbit"
// written as "2mbps" would silently be eight times too large.
func normalizeRate(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "0", "off", "none":
		return "", nil
	}
	for _, unit := range []string{"gbit", "mbit", "kbit", "bit"} {
		num, ok := strings.CutSuffix(v, unit)
		if !ok {
			continue
		}
		num = strings.TrimSpace(num)
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return "", fmt.Errorf("rate %q: %q is not a number", v, num)
		}
		if f <= 0 {
			return "", fmt.Errorf("rate %q must be positive (use \"off\" to disable shaping)", v)
		}
		return num + unit, nil
	}
	return "", fmt.Errorf("rate %q needs a unit: bit, kbit, mbit or gbit (not mbps -- that is bytes and would be 8x too large)", v)
}

// parseRateKbit turns a validated rate string into kbit, for the arithmetic the
// auto-rate controller does. Kept next to normalizeRate so the two cannot
// disagree about what a unit means -- the whole reason "mbps" is rejected.
func parseRateKbit(v string) (int, error) {
	norm, err := normalizeRate(v)
	if err != nil {
		return 0, err
	}
	if norm == "" {
		return 0, nil
	}
	// Longest suffix first, and a slice not a map: with map iteration order,
	// "2000kbit" could match "bit" first and parse "2000k" as a float. That bug
	// was random -- it depended on Go's map ordering from run to run.
	for _, u := range []struct {
		unit string
		mult float64
	}{{"gbit", 1e6}, {"mbit", 1e3}, {"kbit", 1}, {"bit", 1e-3}} {
		unit, mult := u.unit, u.mult
		num, ok := strings.CutSuffix(norm, unit)
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, fmt.Errorf("rate %q: %w", v, err)
		}
		kbit := int(f * mult)
		if kbit < 1 {
			return 0, fmt.Errorf("rate %q is under 1 kbit", v)
		}
		return kbit, nil
	}
	return 0, fmt.Errorf("rate %q has no recognised unit", v)
}

// shapeStats is what the daemon logs so the cap can be judged from the events
// file rather than assumed to be working. Non-zero drops and overlimits are the
// evidence that traffic really was exceeding the cap -- that is, that the cap is
// absorbing load the head unit would otherwise have had to.
type shapeStats struct {
	Dropped    int
	Overlimits int
	BacklogPkt int
}

// parseShapeStats reads the HTB class line out of `tc -s class show dev X`:
//
//	class htb 1:10 root leaf 10: prio 0 rate 2Mbit ceil 2Mbit burst 1600b ...
//	 Sent 13244994 bytes 12338 pkt (dropped 611, overlimits 7928 requeues 0)
//	 backlog 0b 0p requeues 0
//
// Anything it cannot find stays zero; a tc that prints something else must not
// make the daemon fail, only make the log less useful.
func parseShapeStats(out string) shapeStats {
	var st shapeStats
	for _, line := range strings.Split(out, "\n") {
		f := strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == ',' || r == '(' || r == ')' || r == '\t'
		})
		for i, w := range f {
			if i+1 >= len(f) {
				break
			}
			n, err := strconv.Atoi(strings.TrimRight(f[i+1], "bp"))
			if err != nil {
				continue
			}
			switch w {
			case "dropped":
				st.Dropped = n
			case "overlimits":
				st.Overlimits = n
			case "backlog":
				// "backlog 0b 0p": the packet count is the second field.
				if i+2 < len(f) {
					if p, err := strconv.Atoi(strings.TrimRight(f[i+2], "p")); err == nil {
						st.BacklogPkt = p
					}
				}
			}
		}
	}
	return st
}
