package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Scenarios for the review's P0 items: upstream health gating, car-link
// isolation, and RFC 4861 RA timing checked from a packet capture on the box.

func upstreamLoss(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	if err := s.must(E + " upstream down && " + E + " wait phone-noroute A 30"); err != nil {
		return m, err
	}
	s.after(m, "upstream_down→phone_noroute", "upstream", "down", "phone_noroute")
	// The box must stay on the car (a later recovery is then instant).
	if err := s.must("sleep 5 && " + E + " wait box-assoc 1 && " + E + " upstream up && " + E + " wait phone-route A 30"); err != nil {
		return m, err
	}
	s.after(m, "upstream_up→phone_route", "upstream", "up", "phone_route")
	s.pull()
	m["leaves"] = float64(count(s, "carplayd_leave") + count(s, "carplayd_link_lost"))
	if m["leaves"] > 0 {
		return m, fmt.Errorf("carplayd left the car on an upstream failure")
	}
	if count(s, "carplayd_withdraw") < 1 || count(s, "carplayd_resume") < 1 {
		return m, fmt.Errorf("no carplayd_withdraw/resume events")
	}
	s.sh(E + " phone A check || (sleep 1; " + E + " phone A check)")
	return m, nil
}

// isolationRun probes from phone A; with the firewall on only the internet
// may answer. leaks = blocked-expected targets that answered.
func isolationRun(s *session, firewall string) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must("CARPLAYD_FIREWALL=" + firewall + " " + s.boxCmd("A") + " && " + E + " car A on && " + E + " wait box-assoc 60"); err != nil {
		return m, err
	}
	if err := s.must(E + " phone A join && " + E + " wait phone-assoc A 30 && " + E + " wait phone-route A 60 && sleep 1"); err != nil {
		return m, err
	}
	s.sh(E + " isolation A")
	leaks, internet := 0, 0
	for _, e := range s.events {
		if e.Name != "reach" || len(e.Args) < 2 {
			continue
		}
		if strings.HasPrefix(e.Args[0], "internet_") {
			if e.Args[1] == "open" {
				internet++
			}
		} else if e.Args[1] == "open" {
			leaks++
		}
	}
	m["internet_reachable"] = float64(internet)
	m["leaks"] = float64(leaks)
	if internet != 2 {
		return m, fmt.Errorf("internet not reachable from the phone (%d/2)", internet)
	}
	return m, nil
}

func isolation(s *session) (map[string]float64, error) {
	m, err := isolationRun(s, "nft")
	if err == nil && m["leaks"] > 0 {
		err = fmt.Errorf("%v blocked-expected targets answered", m["leaks"])
	}
	return m, err
}

// isolationControl proves the probe can see a leak: same run, firewall off.
func isolationControl(s *session) (map[string]float64, error) {
	m, err := isolationRun(s, "none")
	m["leaks_without_firewall"] = m["leaks"]
	delete(m, "leaks")
	if err == nil && m["leaks_without_firewall"] == 0 {
		err = fmt.Errorf("no leaks with the firewall off: the probe is blind")
	}
	return m, err
}

// raPkt is one RS or RA from `tcpdump -n -tt -v`.
type raPkt struct {
	t          float64
	src, dst   string
	ra         bool
	routerLife int // -1 unknown
	dnsLife    int
	prefLife   int
}

var (
	hdrRE    = regexp.MustCompile(`^(\d+\.\d+) IP6 .*\) (\S+) > (\S+): \[icmp6 sum ok\] ICMP6, router (advertisement|solicitation)`)
	rlifeRE  = regexp.MustCompile(`router lifetime (\d+)s`)
	rdnssRE  = regexp.MustCompile(`rdnss option .*lifetime (\d+)s`)
	prefRE   = regexp.MustCompile(`pref\. time (\d+)s`)
	hdrAnyRE = regexp.MustCompile(`^\d+\.\d+ IP6 `)
)

func parseCapture(out string) []raPkt {
	_, body, ok := strings.Cut(out, "----- capture\n")
	if !ok {
		return nil
	}
	var pkts []raPkt
	var cur *raPkt
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	for _, l := range strings.Split(body, "\n") {
		if hdrAnyRE.MatchString(l) {
			cur = nil
			if h := hdrRE.FindStringSubmatch(l); h != nil {
				t, _ := strconv.ParseFloat(h[1], 64)
				pkts = append(pkts, raPkt{t: t, src: h[2], dst: h[3], ra: h[4] == "advertisement", routerLife: -1, dnsLife: -1, prefLife: -1})
				cur = &pkts[len(pkts)-1]
			}
			continue
		}
		if cur == nil {
			continue
		}
		if v := rlifeRE.FindStringSubmatch(l); v != nil {
			cur.routerLife = atoi(v[1])
		}
		if v := rdnssRE.FindStringSubmatch(l); v != nil {
			cur.dnsLife = atoi(v[1])
		}
		if v := prefRE.FindStringSubmatch(l); v != nil {
			cur.prefLife = atoi(v[1])
		}
	}
	return pkts
}

// auditRAs checks a capture against RFC 4861/7772/8106 and returns metrics.
func auditRAs(pkts []raPkt) (map[string]float64, []string) {
	m := map[string]float64{}
	var problems []string
	box := ""
	for _, p := range pkts {
		if p.ra {
			box = p.src
			break
		}
	}
	var lastMcast float64 = -1
	minGap := 1e9
	for _, p := range pkts {
		if !p.ra || p.src != box {
			continue
		}
		m["ras"]++
		if p.dst == "ff02::1" {
			m["ras_multicast"]++
			if lastMcast >= 0 {
				g := p.t - lastMcast
				minGap = min(minGap, g)
				if g < 2.98 { // MIN_DELAY_BETWEEN_RAS, less capture jitter
					m["mcast_spacing_violations"]++
					problems = append(problems, fmt.Sprintf("multicast RAs %.3f s apart at %.3f", g, p.t))
				}
			}
			lastMcast = p.t
		} else {
			m["ras_unicast"]++
		}
		if p.routerLife == 0 {
			m["withdrawals"]++
			if p.dnsLife > 0 || p.prefLife > 0 {
				m["incomplete_withdrawals"]++
				problems = append(problems, fmt.Sprintf("router lifetime 0 but RDNSS %d s / preferred %d s at %.3f", p.dnsLife, p.prefLife, p.t))
			}
		} else if p.dnsLife > p.routerLife {
			problems = append(problems, fmt.Sprintf("RDNSS lifetime %d s > router lifetime %d s", p.dnsLife, p.routerLife))
		}
	}
	if minGap < 1e9 {
		m["mcast_min_gap_s"] = minGap
	}
	// Every RS from a host with an address: answered by unicast within 0.5 s.
	maxDelay := 0.0
	for i, rs := range pkts {
		if rs.ra || rs.src == box || rs.src == "::" {
			continue
		}
		m["rs_seen"]++
		answered := false
		for _, p := range pkts[i+1:] {
			if p.t-rs.t > 4.5 {
				break
			}
			if p.ra && p.src == box && (p.dst == rs.src || p.dst == "ff02::1") {
				if p.dst == rs.src {
					m["rs_unicast_answers"]++
				}
				maxDelay = max(maxDelay, p.t-rs.t)
				answered = true
				break
			}
		}
		if !answered {
			m["rs_unanswered"]++
			problems = append(problems, fmt.Sprintf("RS from %s at %.3f unanswered", rs.src, rs.t))
		}
	}
	m["rs_answer_max_s"] = maxDelay
	return m, problems
}

func raAudit(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(s.boxCmd("A") + " && " + E + " car A on && " + E + " wait box-assoc 60 && " + E + " capture start"); err != nil {
		return m, err
	}
	steps := []string{
		E + " phone A join && " + E + " wait phone-assoc A 30 && " + E + " wait phone-route A 60",
		"sleep 14", // periodic multicasts
		// Solicitations from a host with an address: each answered by unicast
		// within 0.5 s, and a burst must not turn into a multicast burst.
		E + " phone A rs && sleep 2 && " + E + " phone A rs && " + E + " phone A rs && " + E + " phone A rs && sleep 2",
		E + " phone A leave && sleep 1 && " + E + " phone A join && " + E + " wait phone-assoc A 30 && " + E + " wait phone-route A 60",
		E + " upstream down && " + E + " wait phone-noroute A 30 && sleep 4 && " + E + " upstream up && " + E + " wait phone-route A 30",
		ramp("A", -48, -96, -3) + " & " + E + " wait phone-noroute A 90; wait",
	}
	for _, st := range steps {
		if err := s.must(st); err != nil {
			s.sh(E + " capture stop")
			return m, err
		}
	}
	out, _ := s.sh(E + " capture stop")
	pkts := parseCapture(out)
	if len(pkts) == 0 {
		return m, fmt.Errorf("empty capture")
	}
	am, problems := auditRAs(pkts)
	for k, v := range am {
		m[k] = v
	}
	if m["withdrawals"] < 2 {
		problems = append(problems, fmt.Sprintf("only %v withdrawal RAs (want upstream loss + goodbye)", m["withdrawals"]))
	}
	if m["rs_unicast_answers"] < 1 {
		problems = append(problems, "no RS answered by unicast")
	}
	if len(problems) > 0 {
		return m, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return m, nil
}
