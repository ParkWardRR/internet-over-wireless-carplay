// carplayd gives iPhones internet over a car's wireless-CarPlay Wi-Fi.
//
// It runs on a box near the parked cars (OpenWrt or Debian), joins whichever
// configured car's CarPlay AP appears, and advertises an IPv6 route and DNS on
// it so the phone can reach the internet through the box (NAT66 is configured
// separately: fw4 masq6 on OpenWrt). It leaves cleanly when the link starts to
// fail, with a router-lifetime-0 "goodbye", and rejoins when the car returns.
// See docs/two-cars.md.
//
// One radio serves one car at a time. Give each car its own `phy` and they are
// served at once, each on its own link; cars sharing a radio take turns on it.
//
//	carplayd -c /etc/carplayd.conf
//	carplayd -c /etc/carplayd.conf -check     # validate and print the wpa config
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	path := flag.String("c", "/etc/carplayd.conf", "config file")
	check := flag.Bool("check", false, "validate the config, print the wpa_supplicant config (PSKs masked) and exit")
	// Separate from -check so it can be piped straight into `nft -c -f -`.
	nftOut := flag.Bool("nft", false, "print the nftables ruleset this config produces and exit")
	flag.Parse()

	f, err := os.Open(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "carplayd:", err)
		os.Exit(1)
	}
	cfg, err := parseConfig(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "carplayd: config:", err)
		os.Exit(1)
	}
	// The ruleset on its own, so nft itself can check it before the box ever
	// applies it: `carplayd -nft | nft -c -f -`. Until this existed the ruleset
	// was only visible after installation, which is a poor moment to find a
	// syntax error -- an nft failure at startup leaves the car link with no
	// isolation and no NAT.
	if *nftOut {
		fmt.Print(nftRuleset(cfg, nil))
		return
	}
	if *check {
		printCheck(cfg)
		return
	}

	// Shared across every link: one log writer (unsynchronised writes from
	// several links interleave mid-line), one metrics registry, one set of
	// learned profiles backed by one file.
	out := &evWriter{}
	if cfg.EventsPath != "" {
		out.f, _ = os.OpenFile(cfg.EventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	}
	m := newMetrics()
	if cfg.Metrics != "" {
		if err := m.listen(cfg.Metrics); err != nil {
			fmt.Fprintln(os.Stderr, "carplayd: metrics:", err)
			os.Exit(1)
		}
	}
	prof := loadProfiles(cfg.StatePath)

	// Register every configured car up front. Without this the gauges for a
	// car appear only once it has been served, so a dashboard or an alert on
	// "car B not serving" has nothing to read until the first time it works --
	// exactly when you want to be told.
	for _, c := range cfg.Cars {
		m.setCar(c.ID, func(*carMetric) {})
	}

	links := buildLinks(cfg, out, m, prof)

	// Upstream reachability is a property of the box, not of a link, so it is
	// probed once and handed to every link. Ten links each running their own
	// probe every few seconds would be pure waste, and they could disagree.
	initial := upstream{ok: true, detail: "health off"}
	if cfg.Health {
		initial = checkUpstream(cfg.HealthTargets, healthTimeout)
	}
	for _, d := range links {
		d.up.ok = initial.ok
	}
	links[0].ev("upstream", map[bool]string{true: "ok", false: "down"}[initial.ok], initial.detail)

	for _, d := range links {
		if err := d.setup(); err != nil {
			fmt.Fprintf(os.Stderr, "carplayd: setup %s: %v\n", d.label, err)
			os.Exit(1)
		}
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Fan the signal out: each link's loop takes its own stop channel.
	stops := make([]chan os.Signal, len(links))
	for i := range links {
		stops[i] = make(chan os.Signal, 1)
	}
	done := make(chan struct{})
	go func() {
		<-stop
		for _, s := range stops {
			s <- syscall.SIGTERM
		}
		close(done)
	}()

	if cfg.Health {
		go pollUpstream(cfg, links, done)
	}

	var wg sync.WaitGroup
	for i, d := range links {
		wg.Add(1)
		go func(d *daemon, s chan os.Signal) {
			defer wg.Done()
			d.loop(s)
		}(d, stops[i])
	}
	wg.Wait()
}

// pollUpstream probes the internet once per interval and gives the result to
// every link. A link that is mid-check is skipped rather than blocked, so one
// slow link cannot hold up the others.
// buildLinks makes one daemon per radio. Each gets only its own cars, phy, STA
// and control directory; all of them share the log writer, the metrics registry
// and the learned profiles. The first link owns the box-wide resources -- the
// nftables table that has to name every car link, NAT, and persisting the
// shared learned state -- so they are built once rather than N times.
func buildLinks(cfg Config, out *evWriter, m *metrics, prof *profiles) []*daemon {
	groups := cfg.radios()
	links := make([]*daemon, 0, len(groups))
	for i, cars := range groups {
		lc := cfg
		lc.Cars = cars
		lc.Phy, lc.STA = cars[0].Phy, cars[0].STA
		lc.CtrlDir = cfg.ctrlDirFor(cars[0].Phy)
		links = append(links, &daemon{
			cfg: lc, fw: cfg, owner: i == 0, label: cars[0].STA,
			out: out, m: m, prof: prof,
			held: map[string]bool{}, firstSeen: map[string]time.Time{},
			lastSurvey: map[string]time.Time{}, relaxed: map[string]int{},
			leftAt:  map[string]time.Time{},
			wpaLast: map[string]time.Time{}, wpaSame: map[string]int{},
			healthC: make(chan upstream, 1),
		})
	}
	return links
}

func pollUpstream(cfg Config, links []*daemon, done <-chan struct{}) {
	t := time.NewTicker(cfg.HealthEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		serving := false
		for _, d := range links {
			if d.servingNow() {
				serving = true
				break
			}
		}
		if !serving {
			continue // nothing to withdraw a route from
		}
		r := checkUpstream(cfg.HealthTargets, healthTimeout)
		for _, d := range links {
			select {
			case d.healthC <- r:
			default:
			}
		}
	}
}

func printCheck(cfg Config) {
	masked := cfg
	masked.Cars = append([]Car(nil), cfg.Cars...)
	for i := range masked.Cars {
		masked.Cars[i].PSK = "********"
	}
	for i, cars := range cfg.radios() {
		lc := masked
		lc.Cars = nil
		for _, c := range cars {
			for _, mc := range masked.Cars {
				if mc.ID == c.ID {
					lc.Cars = append(lc.Cars, mc)
				}
			}
		}
		lc.CtrlDir = cfg.ctrlDirFor(cars[0].Phy)
		fmt.Printf("# link %d: phy %s sta %s ctrl %s\n", i+1, cars[0].Phy, cars[0].STA, lc.CtrlDir)
		fmt.Print(wpaConfig(lc))
	}
	for _, c := range cfg.Cars {
		fmt.Printf("# car %s: %q ch %d (%d MHz) phy %s sta %s prefix %s join %d leave %d rejoin %d drop %d fail_rate %d priority %d\n",
			c.ID, c.SSID, c.Channel, c.Freq(), c.Phy, c.STA, c.Prefix,
			c.Join, c.Leave, c.Rejoin, c.Drop, c.FailRate, c.Priority)
	}
}
