// carplayd gives iPhones internet over a car's wireless-CarPlay Wi-Fi.
//
// It runs on a box near the parked cars (OpenWrt or Debian), joins whichever
// configured car's CarPlay access point appears, and advertises an IPv6 route
// and DNS on it so the phone can reach the internet through the box (NAT66 is
// configured separately: fw4 masq6 on OpenWrt). It leaves cleanly when the
// car's signal fades, with a router-lifetime-0 "goodbye", and rejoins when it
// returns. See docs/two-cars.md.
//
//	carplayd -c /etc/carplayd.conf
//	carplayd -c /etc/carplayd.conf -check     # validate and print the wpa config
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("c", "/etc/carplayd.conf", "config file")
	check := flag.Bool("check", false, "validate the config, print the wpa_supplicant config (PSKs masked) and exit")
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
	if *check {
		masked := cfg
		masked.Cars = append([]Car(nil), cfg.Cars...)
		for i := range masked.Cars {
			masked.Cars[i].PSK = "********"
		}
		fmt.Print(wpaConfig(masked))
		for _, c := range cfg.Cars {
			fmt.Printf("# car %s: %q ch %d (%d MHz) prefix %s join %d leave %d rejoin %d priority %d\n",
				c.ID, c.SSID, c.Channel, c.Freq(), c.Prefix, c.Join, c.Leave, c.Rejoin, c.Priority)
		}
		return
	}

	d := &daemon{cfg: cfg, held: map[string]bool{}, firstSeen: map[string]time.Time{}, lastSurvey: map[string]time.Time{}, relaxed: map[string]int{}, m: newMetrics(), prof: loadProfiles(cfg.StatePath)}
	if cfg.Metrics != "" {
		if err := d.m.listen(cfg.Metrics); err != nil {
			fmt.Fprintln(os.Stderr, "carplayd: metrics:", err)
			os.Exit(1)
		}
	}
	if cfg.EventsPath != "" {
		d.evFile, _ = os.OpenFile(cfg.EventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	}
	if err := d.setup(); err != nil {
		fmt.Fprintln(os.Stderr, "carplayd: setup:", err)
		os.Exit(1)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	d.loop(stop)
}
