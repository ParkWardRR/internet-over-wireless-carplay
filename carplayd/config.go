package main

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Car is one car's CarPlay network and how we treat it.
type Car struct {
	ID       string       // "A", "B": names the [car X] section
	SSID     string       // e.g. "BMW00001 CarPlay"
	PSK      string       // 10 characters on the NBT EVO
	Channel  int          // 2.4 GHz channel number
	BSSID    string       // optional: only join this AP
	Prefix   netip.Prefix // the ULA /64 we advertise on this car's network
	Join     int          // dBm: join when the car is at least this strong
	Leave    int          // dBm: leave when weaker than this for LeaveFor
	Rejoin   int          // dBm: after leaving, rejoin only at least this strong
	Priority int          // higher wins when both cars are seen
	Drop     int          // dB: also leave when the signal falls this far below where it sat 5-20 s ago (0 = off)
}

// Freq returns the 2.4 GHz centre frequency in MHz, or 0 when the channel is
// unknown (channel = 0: hunt on every 2.4 GHz channel).
func (c Car) Freq() int {
	if c.Channel == 0 {
		return 0
	}
	if c.Channel == 14 {
		return 2484
	}
	return 2407 + 5*c.Channel
}

// Config is carplayd's whole configuration.
type Config struct {
	Phy           string        // wiphy to put our STA on, e.g. phy0
	STA           string        // name of the STA netdev we create
	CtrlDir       string        // wpa_supplicant control directory for our instance
	Hunt          time.Duration // scan period while not serving
	Poll          time.Duration // signal poll period while serving
	LeaveFor      time.Duration // how long below Leave before leaving
	RAInterval    time.Duration // MaxRtrAdvInterval: periodic RAs every random [0.75, 1] × this (RFC 4861)
	RouterLife    time.Duration // RA router lifetime
	DNS           []netip.Addr  // RDNSS servers
	EventsPath    string        // EV lines are appended here too
	WPABinary     string
	Uplink        string         // interface toward the internet (for nat66)
	NAT66         string         // "none" (fw4 or someone else does it), "ip6tables" or "nft"
	Firewall      string         // "nft": isolate the car networks (carplayd table); "none"
	Block         []netip.Prefix // extra destinations phones must not reach (home LAN beyond the uplink)
	Health        bool           // withdraw the route while the upstream is unreachable
	HealthEvery   time.Duration
	HealthFail    int           // consecutive failed checks before withdrawing
	HealthTargets []string      // host:port to TCP-connect; default: each DNS server, port 53
	IdleSwitch    time.Duration // serve another car once the served one has had no phone this long
	Survey        time.Duration // lab: log each visible car's signal this often ("sighting", "signal" events); 0 = off
	Metrics       string        // Prometheus listen address, e.g. 127.0.0.1:9101 ("" = off)
	Debug         bool          // log every wpa event, full scan results, all link counters and the leave decision each poll
	Cars          []Car
}

func defaultConfig() Config {
	return Config{
		Phy: "phy0", STA: "phy0-sta0", CtrlDir: "/var/run/carplayd",
		Hunt: time.Second, Poll: time.Second, LeaveFor: 3 * time.Second,
		RAInterval: 4 * time.Second, RouterLife: 20 * time.Second, // 20 s: an abrupt drive-out (no goodbye possible) strands the phone at most this long
		DNS:       []netip.Addr{netip.MustParseAddr("2001:4860:4860::8888"), netip.MustParseAddr("2606:4700:4700::1111")},
		WPABinary: "wpa_supplicant",
		NAT66:     "none", IdleSwitch: time.Minute,
		Firewall: "nft", Health: true, HealthEvery: 3 * time.Second, HealthFail: 2,
	}
}

func defaultCar(id string) Car {
	return Car{ID: id, Channel: 11, Join: -80, Leave: -84, Rejoin: -76}
}

// parseConfig reads an INI-style file:
//
//	phy = phy0
//	dns = 2001:db8::53
//	[car A]
//	ssid = BMW00001 CarPlay
//	psk = ...
//	channel = 11
//	prefix = fd5c:ba1e:f32:a::/64
func parseConfig(r io.Reader) (Config, error) {
	cfg := defaultConfig()
	var car *Car
	dnsSet := false
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			f := strings.Fields(strings.Trim(l, "[]"))
			if len(f) != 2 || f[0] != "car" {
				return cfg, fmt.Errorf("line %d: want [car <id>]", line)
			}
			cfg.Cars = append(cfg.Cars, defaultCar(f[1]))
			car = &cfg.Cars[len(cfg.Cars)-1]
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return cfg, fmt.Errorf("line %d: want key = value", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		var err error
		if car == nil {
			err = setGlobal(&cfg, k, v, &dnsSet)
		} else {
			err = setCar(car, k, v)
		}
		if err != nil {
			return cfg, fmt.Errorf("line %d: %v", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return cfg, err
	}
	if cfg.NAT66 != "none" && cfg.Uplink == "" {
		return cfg, fmt.Errorf("nat66 = %s needs uplink = <interface>", cfg.NAT66)
	}
	if cfg.RAInterval < minAllowedMaxRtrAdv || cfg.RAInterval > 1800*time.Second {
		return cfg, fmt.Errorf("ra_interval (MaxRtrAdvInterval) must be 4s..1800s (RFC 4861)")
	}
	if cfg.RouterLife != 0 && (cfg.RouterLife < cfg.RAInterval || cfg.RouterLife > 9000*time.Second) {
		return cfg, fmt.Errorf("router_lifetime must be 0 or ra_interval..9000s (RFC 4861)")
	}
	if len(cfg.HealthTargets) == 0 {
		for _, d := range cfg.DNS {
			cfg.HealthTargets = append(cfg.HealthTargets, netip.AddrPortFrom(d, 53).String())
		}
	}
	if len(cfg.Cars) == 0 {
		return cfg, fmt.Errorf("no [car] sections")
	}
	for i := range cfg.Cars {
		c := &cfg.Cars[i]
		if c.SSID == "" || len(c.PSK) < 8 {
			return cfg, fmt.Errorf("car %s: ssid and psk (8+ chars) are required", c.ID)
		}
		if !c.Prefix.IsValid() {
			// fd5c:ba1e:f32:<index+a>::/64 unless configured
			c.Prefix = netip.MustParsePrefix(fmt.Sprintf("fd5c:ba1e:f32:%x::/64", 0xa+i))
		}
		if c.Prefix.Bits() != 64 || !c.Prefix.Addr().Is6() {
			return cfg, fmt.Errorf("car %s: prefix must be an IPv6 /64", c.ID)
		}
		if !(c.Rejoin >= c.Join && c.Join > c.Leave) {
			return cfg, fmt.Errorf("car %s: want rejoin >= join > leave (hysteresis), got %d/%d/%d", c.ID, c.Rejoin, c.Join, c.Leave)
		}
	}
	return cfg, nil
}

func setGlobal(cfg *Config, k, v string, dnsSet *bool) error {
	var err error
	switch k {
	case "phy":
		cfg.Phy = v
	case "sta":
		cfg.STA = v
	case "ctrl_dir":
		cfg.CtrlDir = v
	case "events":
		cfg.EventsPath = v
	case "survey":
		cfg.Survey, err = time.ParseDuration(v)
	case "idle_switch":
		cfg.IdleSwitch, err = time.ParseDuration(v)
	case "metrics":
		cfg.Metrics = v
	case "debug":
		if v != "on" && v != "off" {
			return fmt.Errorf("debug must be on or off")
		}
		cfg.Debug = v == "on"
	case "uplink":
		cfg.Uplink = v
	case "nat66":
		if v != "none" && v != "ip6tables" && v != "nft" {
			return fmt.Errorf("nat66 must be none, ip6tables or nft")
		}
		cfg.NAT66 = v
	case "firewall":
		if v != "none" && v != "nft" {
			return fmt.Errorf("firewall must be nft or none")
		}
		cfg.Firewall = v
	case "block":
		for _, s := range splitList(v) {
			p, perr := netip.ParsePrefix(s)
			if perr != nil || !p.Addr().Is6() {
				return fmt.Errorf("block %q: not an IPv6 prefix", s)
			}
			cfg.Block = append(cfg.Block, p.Masked())
		}
	case "health":
		if v != "on" && v != "off" {
			return fmt.Errorf("health must be on or off")
		}
		cfg.Health = v == "on"
	case "health_interval":
		cfg.HealthEvery, err = time.ParseDuration(v)
	case "health_fail":
		cfg.HealthFail, err = strconv.Atoi(v)
	case "health_target":
		for _, s := range splitList(v) {
			if _, perr := netip.ParseAddrPort(s); perr != nil {
				return fmt.Errorf("health_target %q: want [addr]:port", s)
			}
			cfg.HealthTargets = append(cfg.HealthTargets, s)
		}
	case "wpa_supplicant":
		cfg.WPABinary = v
	case "hunt":
		cfg.Hunt, err = time.ParseDuration(v)
	case "poll":
		cfg.Poll, err = time.ParseDuration(v)
	case "leave_for":
		cfg.LeaveFor, err = time.ParseDuration(v)
	case "ra_eager":
		return fmt.Errorf("ra_eager was removed: RAs follow RFC 4861 timing (ra_interval)")
	case "ra_interval":
		cfg.RAInterval, err = time.ParseDuration(v)
	case "router_lifetime":
		cfg.RouterLife, err = time.ParseDuration(v)
	case "dns":
		if !*dnsSet {
			cfg.DNS, *dnsSet = nil, true
		}
		for _, s := range splitList(v) {
			a, perr := netip.ParseAddr(s)
			if perr != nil || !a.Is6() {
				return fmt.Errorf("dns %q: not an IPv6 address", s)
			}
			cfg.DNS = append(cfg.DNS, a)
		}
	default:
		return fmt.Errorf("unknown key %q", k)
	}
	return err
}

func setCar(c *Car, k, v string) error {
	var err error
	atoi := func(s string) int {
		n, e := strconv.Atoi(s)
		if e != nil {
			err = fmt.Errorf("%s: %v", k, e)
		}
		return n
	}
	switch k {
	case "ssid":
		c.SSID = v
	case "psk":
		c.PSK = v
	case "channel":
		c.Channel = atoi(v)
		if err == nil && (c.Channel < 0 || c.Channel > 14) {
			err = fmt.Errorf("channel %d is not 2.4 GHz (0 = any)", c.Channel)
		}
	case "bssid":
		c.BSSID = strings.ToLower(v)
	case "prefix":
		c.Prefix, err = netip.ParsePrefix(v)
	case "join":
		c.Join = atoi(v)
	case "leave":
		c.Leave = atoi(v)
	case "rejoin":
		c.Rejoin = atoi(v)
	case "priority":
		c.Priority = atoi(v)
	case "drop":
		c.Drop = atoi(v)
	default:
		return fmt.Errorf("unknown car key %q", k)
	}
	return err
}

func splitList(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
}
