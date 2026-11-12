package main

import (
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const healthTimeout = 2 * time.Second

// dnsLife is the RDNSS lifetime: 3 × MaxRtrAdvInterval (RFC 8106 5.1), but
// never longer than the router lifetime, so DNS servers don't outlive the route.
func dnsLife(c Config) time.Duration {
	l := 3 * c.RAInterval
	if c.RouterLife < l {
		l = c.RouterLife
	}
	return l
}

// upstream is one health check: can the box reach the internet itself?
type upstream struct {
	ok     bool
	detail string // the target that answered, or why none did
}

// checkUpstream TCP-connects to every target at once; one answer is enough.
// The DNS servers we hand out are the default targets, so "healthy" means
// "the phone's DNS would work through us".
func checkUpstream(targets []string, timeout time.Duration) upstream {
	if len(targets) == 0 {
		return upstream{ok: true, detail: "no targets"}
	}
	res := make(chan upstream, len(targets))
	for _, t := range targets {
		go func(t string) {
			c, err := net.DialTimeout("tcp", t, timeout)
			if err != nil {
				res <- upstream{detail: err.Error()}
				return
			}
			c.Close()
			res <- upstream{ok: true, detail: t}
		}(t)
	}
	var last upstream
	for range targets {
		if last = <-res; last.ok {
			return last
		}
	}
	return last
}

func (d *daemon) onHealth(r upstream) {
	d.lastCheck = time.Now()
	if !d.up.update(r.ok, d.cfg.HealthFail) {
		return
	}
	d.m.set(func(m *metrics) { m.upstream = d.up.ok })
	if d.up.ok {
		d.ev("upstream_up", r.detail)
	} else {
		d.ev("upstream_down", r.detail)
		d.m.inc("upstream_down")
	}
	if d.ra != nil {
		d.ra.setWithdrawn(!d.up.ok, d.neighbours())
		d.ev(map[bool]string{true: "resume", false: "withdraw"}[d.up.ok], d.serving.ID)
	}
}

// freshHealth makes sure d.up is current before we start advertising.
func (d *daemon) freshHealth() {
	if !d.cfg.Health || time.Since(d.lastCheck) < d.cfg.HealthEvery {
		return
	}
	r := checkUpstream(d.cfg.HealthTargets, healthTimeout)
	d.lastCheck = time.Now()
	if r.ok != d.up.ok {
		d.up = health{ok: r.ok}
		d.m.set(func(m *metrics) { m.upstream = r.ok })
		d.ev(map[bool]string{true: "upstream_up", false: "upstream_down"}[r.ok], r.detail)
	}
}

// neighbours lists one address per device on the served car's link.
func (d *daemon) neighbours() []netip.Addr {
	if d.serving == nil {
		return nil
	}
	out, err := exec.Command("ip", "-6", "neigh", "show", "dev", d.cfg.STA).Output()
	if err != nil {
		return nil
	}
	return withdrawTargets(string(out), d.serving.Prefix)
}

// homeBlock is what phones must not reach beyond the generic private ranges:
// the uplink's own networks (the home LAN) plus `block`.
func (d *daemon) homeBlock() []netip.Prefix {
	if d.cfg.Uplink == "" {
		return slices.Clone(d.cfg.Block)
	}
	out, err := exec.Command("ip", "-6", "route", "show", "dev", d.cfg.Uplink).Output()
	if err != nil {
		return slices.Clone(d.cfg.Block)
	}
	return homePrefixes(string(out), d.cfg.Block)
}

func (d *daemon) firewallOn() bool { return d.cfg.Firewall == "nft" || d.cfg.NAT66 == "nft" }

func (d *daemon) applyFirewall(block []netip.Prefix) error {
	if !d.firewallOn() {
		return nil
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(nftRuleset(d.fw, block))
	if out, err := cmd.CombinedOutput(); err != nil {
		return &cmdError{"nft -f -", err, strings.TrimSpace(string(out))}
	}
	d.blocked = block
	var s []string
	for _, p := range block {
		s = append(s, p.String())
	}
	d.ev("firewall", d.cfg.Firewall, "nat66="+d.cfg.NAT66, "block="+strings.Join(s, ","))
	return nil
}

// refreshFirewall re-applies the table if the home prefixes changed (new
// delegation) or someone flushed it (a firewall reload).
func (d *daemon) refreshFirewall() {
	if !d.firewallOn() {
		return
	}
	block := d.homeBlock()
	gone := exec.Command("nft", "list", "table", "inet", "carplayd").Run() != nil
	if gone || !slices.Equal(block, d.blocked) {
		if err := d.applyFirewall(block); err != nil {
			d.ev("error", "firewall", err.Error())
		}
	}
}

type cmdError struct {
	cmd string
	err error
	out string
}

func (e *cmdError) Error() string { return e.cmd + ": " + e.err.Error() + ": " + e.out }
