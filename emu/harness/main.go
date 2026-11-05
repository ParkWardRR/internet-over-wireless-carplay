// emuharness runs the garage scenarios against the emulated box firmware on
// build-host and reports how long each step takes.
//
//	emuharness -box stock -scenarios all -n 5       # netifd/odhcpd + openwrt-carplay.sh
//	emuharness -box carplayd -scenarios all -n 5    # carplayd owns the radio
//	emuharness -list
//
// Each repetition boots a fresh VM (emu/run.sh boot), builds the topology
// (emu.sh setup), runs the scenario's steps over SSH and collects the
// "EV <t> <event> ..." lines (emu.sh, emuagent and carplayd all use CLOCK_BOOTTIME,
// so their events share one timeline). Metrics are differences between
// events; the report gives min/median/p90/max per metric and checks them
// against the plan's targets. Results go to emu/out/results/<stamp>.{json,md,log}.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type event struct {
	T    float64  `json:"t"`
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// run is one repetition of one scenario.
type run struct {
	Box      string             `json:"box"`
	Scenario string             `json:"scenario"`
	Rep      int                `json:"rep"`
	Events   []event            `json:"events"`
	Metrics  map[string]float64 `json:"metrics"`
	Err      string             `json:"error,omitempty"`
}

type session struct {
	repo     string
	box      string // stock | carplayd
	events   []event
	log      *os.File
	cpdLines int // carplayd.events lines already read
}

func parseEV(out string) []event {
	var evs []event
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && f[0] == "EV" {
			if t, err := strconv.ParseFloat(f[1], 64); err == nil {
				evs = append(evs, event{T: t, Name: f[2], Args: f[3:]})
			}
		}
	}
	return evs
}

// sh runs a command in the VM and collects its EV lines.
func (s *session) sh(cmd string) (string, error) {
	out, err := exec.Command(filepath.Join(s.repo, "emu/run.sh"), "ssh", cmd).CombinedOutput()
	fmt.Fprintf(s.log, "$ %s\n%s", cmd, out)
	s.events = append(s.events, parseEV(string(out))...)
	return string(out), err
}

// must runs steps that have to succeed for the scenario to mean anything.
func (s *session) must(cmd string) error {
	if _, err := s.sh(cmd); err != nil {
		return fmt.Errorf("%q: %v", cmd, err)
	}
	return nil
}

// pull reads carplayd's new events (it runs as a daemon, so its EV lines go
// to a file rather than to our SSH sessions).
func (s *session) pull() {
	if s.box != "carplayd" {
		return
	}
	out, _ := exec.Command(filepath.Join(s.repo, "emu/run.sh"), "ssh",
		fmt.Sprintf("tail -n +%d /tmp/emu/carplayd.events 2>/dev/null", s.cpdLines+1)).CombinedOutput()
	evs := parseEV(string(out))
	s.cpdLines += len(evs)
	s.events = append(s.events, evs...)
	sort.SliceStable(s.events, func(i, j int) bool { return s.events[i].T < s.events[j].T })
}

// boxCmd configures the box for the given cars in the session's box mode.
func (s *session) boxCmd(cars ...string) string {
	if s.box == "carplayd" {
		return E + " box-carplayd " + strings.Join(cars, " ")
	}
	return E + " box-apply " + cars[0]
}

// last returns the last event with this name (and first arg, if given).
func (s *session) last(name string, arg ...string) (event, bool) {
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Name == name && (len(arg) == 0 || (len(e.Args) > 0 && e.Args[0] == arg[0])) {
			return e, true
		}
	}
	return event{}, false
}

// first returns the first event matching pred at or after time t.
func (s *session) first(t float64, pred func(event) bool) (event, bool) {
	for _, e := range s.events {
		if e.T >= t && pred(e) {
			return e, true
		}
	}
	return event{}, false
}

// between records metric = t(to) - t(from) using the most recent of each.
func (s *session) between(m map[string]float64, metric, from, to string, arg ...string) {
	a, ok1 := s.last(from, arg...)
	b, ok2 := s.last(to, arg...)
	if ok1 && ok2 && b.T >= a.T {
		m[metric] = b.T - a.T
	}
}

const E = "/root/carplay/emu.sh"

type scenario struct {
	name, about string
	boxes       string // which box modes it applies to
	fn          func(s *session) (map[string]float64, error)
}

var scenarios = []scenario{
	{"detect", "box hunting, car A starts: box join, phone route, internet", "stock,carplayd", detect},
	{"detect-scanlist", "stock box with scan_list = car channel (2462)", "stock", detectScanList},
	{"box-late", "phone joined first (its RS burst over), then the box joins: first RA timing", "stock,carplayd", boxLate},
	{"drive-out", "car A leaves the box's range abruptly (phone stays in the cabin)", "stock,carplayd", driveOut},
	{"return", "car A comes back into range after a drive-out", "stock,carplayd", returnScenario},
	{"cycle", "car A off/on 5 times with the box hunting", "stock,carplayd", cycle},
	{"fade-out", "car A's signal fades 3 dB/s: leave + goodbye before the link is lost", "carplayd", fadeOut},
	{"fade-return", "after a fade-out, the signal comes back 3 dB/s: rejoin with hysteresis", "carplayd", fadeReturn},
	{"two-cars", "car A served, drives out; car B starts: B served with its own prefix", "carplayd", twoCars},
	{"both-on", "both cars start together: priority picks A", "carplayd", bothOn},
	{"flap", "signal hovers around the leave threshold for 30 s: at most one leave, no rejoin below rejoin", "carplayd", flap},
	{"wpa-kill", "carplayd's wpa_supplicant is killed while serving: restart and serve again", "carplayd", wpaKill},
	{"restart", "carplayd itself is killed and restarted (procd/systemd): serve again, same prefix", "carplayd", restart},
	{"soak", "car A off/on 20 times: every cycle served", "carplayd", soak},
	{"upstream-loss", "the internet behind the box dies while serving: withdraw the route, stay on the car, resume", "carplayd", upstreamLoss},
	{"isolation", "phone on the car Wi-Fi: internet reachable; box, home LAN, other car not", "carplayd", isolation},
	{"isolation-control", "same with the firewall off: the probe must see leaks", "carplayd", isolationControl},
	{"ra-audit", "capture on the box: RFC 4861 RA timing, unicast RS answers, complete withdrawals", "carplayd", raAudit},
	{"trend-leave", "car drives off slowly (1 dB/s): drop = 6 leaves with a goodbye long before -84, then rejoins when it comes back", "carplayd", trendLeave},
}

func up(s *session, cars ...string) error {
	if len(cars) == 0 {
		cars = []string{"A"}
	}
	if err := s.must(s.boxCmd(cars...) + " && " + E + " car A on && " + E + " wait box-assoc 60"); err != nil {
		return err
	}
	return s.must(E + " phone A join && " + E + " wait phone-assoc A 30 && " + E + " wait phone-route A 60")
}

func detectWith(s *session, env string) (map[string]float64, error) {
	m := map[string]float64{}
	// Box first (as in production), car off: the box is hunting.
	if err := s.must(env + s.boxCmd("A") + " && sleep 3"); err != nil {
		return m, err
	}
	jitter() // a real car starts at a random moment relative to the box's scan cycle
	if err := s.must(E + " car A on && " + E + " wait box-assoc 60"); err != nil {
		return m, err
	}
	s.between(m, "car_on→box_assoc", "car_on", "box_assoc")
	// The phone joins a moment later, as an iPhone does after the CarPlay Bluetooth handshake.
	if err := s.must(E + " phone A join && " + E + " wait phone-assoc A 30 && " + E + " wait phone-route A 60"); err != nil {
		return m, err
	}
	s.between(m, "phone_assoc→phone_route", "phone_assoc", "phone_route", "A")
	s.sh(E + " phone A check || (sleep 1; " + E + " phone A check) || (sleep 2; " + E + " phone A check)")
	s.between(m, "phone_route→internet", "phone_route", "phone_internet", "A")
	return m, nil
}

func detect(s *session) (map[string]float64, error) { return detectWith(s, "") }
func detectScanList(s *session) (map[string]float64, error) {
	return detectWith(s, "SCAN_LIST=2462 ")
}

func boxLate(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(E + " car A on && " + E + " phone A join && " + E + " wait phone-assoc A 30"); err != nil {
		return m, err
	}
	// Let the phone's Router Solicitations run out (3 × 4 s) before the box appears.
	if err := s.must("sleep 15 && " + s.boxCmd("A") + " && " + E + " wait box-assoc 60 && " + E + " wait phone-route A 90"); err != nil {
		return m, err
	}
	s.between(m, "box_assoc→phone_route", "box_assoc", "phone_route")
	return m, nil
}

func driveOut(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range out && " + E + " wait box-disassoc 60 && " + E + " wait phone-noroute A 180"); err != nil {
		return m, err
	}
	s.after(m, "out_of_range→box_disassoc", "car_range", "A", "box_disassoc")
	s.after(m, "out_of_range→phone_noroute", "car_range", "A", "phone_noroute")
	return m, nil
}

func returnScenario(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range out && " + E + " wait box-disassoc 60 && sleep 5"); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range in && " + E + " wait box-assoc 60 && " + E + " wait phone-route A 120"); err != nil {
		return m, err
	}
	s.after(m, "back_in_range→box_assoc", "car_range", "A", "box_assoc")
	s.between(m, "box_assoc→phone_route", "box_assoc", "phone_route")
	return m, nil
}

func cycle(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(s.boxCmd("A") + " && sleep 2"); err != nil {
		return m, err
	}
	for i := 1; i <= 5; i++ {
		jitter()
		if err := s.must(E + " car A on && " + E + " wait box-assoc 60"); err != nil {
			return m, err
		}
		s.between(m, fmt.Sprintf("car_on→box_assoc#%d", i), "car_on", "box_assoc")
		if err := s.must(E + " car A off && " + E + " wait box-disassoc 60 && sleep 4"); err != nil {
			return m, err
		}
	}
	return m, nil
}

// ramp emits "car X signal N" once a second from `from` to `to` in `step` dB.
func ramp(car string, from, to, step int) string {
	var vals []string
	for v := from; (step < 0 && v >= to) || (step > 0 && v <= to); v += step {
		vals = append(vals, strconv.Itoa(v))
	}
	return fmt.Sprintf("for v in %s; do %s car %s signal $v; sleep 1; done", strings.Join(vals, " "), E, car)
}

// signalCrossing returns the first car_signal event at/after t whose value is
// below (falling) or at/above (rising) the threshold.
func (s *session) signalCrossing(t float64, car string, threshold int, falling bool) (event, bool) {
	return s.first(t, func(e event) bool {
		if e.Name != "car_signal" || len(e.Args) < 2 || e.Args[0] != car {
			return false
		}
		v, err := strconv.Atoi(e.Args[1])
		return err == nil && ((falling && v < threshold) || (!falling && v >= threshold))
	})
}

const leaveDBm, rejoinDBm = -84, -76 // carplayd defaults

func fadeOut(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	err := s.must(ramp("A", -48, -96, -3) + " & " + E + " wait phone-noroute A 90; wait")
	s.pull()
	if cross, ok := s.signalCrossing(0, "A", leaveDBm, true); ok {
		if g, ok := s.first(cross.T, func(e event) bool { return e.Name == "carplayd_goodbye" }); ok {
			m["below_leave→goodbye"] = g.T - cross.T
		}
		if n, ok := s.first(cross.T, func(e event) bool { return e.Name == "phone_noroute" }); ok {
			m["below_leave→phone_noroute"] = n.T - cross.T
		}
	}
	return m, err
}

func fadeReturn(s *session) (map[string]float64, error) {
	m, err := fadeOut(s)
	if err != nil {
		return m, err
	}
	start := s.events[len(s.events)-1].T
	err = s.must("sleep 2; " + ramp("A", -96, -45, 3) + " & " + E + " wait phone-route A 90; wait")
	s.pull()
	if cross, ok := s.signalCrossing(start, "A", rejoinDBm, false); ok {
		if a, ok := s.first(cross.T, func(e event) bool { return e.Name == "carplayd_connected" }); ok {
			m["above_rejoin→box_assoc"] = a.T - cross.T
		}
		if r, ok := s.first(cross.T, func(e event) bool { return e.Name == "phone_route" }); ok {
			m["above_rejoin→phone_route"] = r.T - cross.T
		}
	}
	// Hysteresis: no rejoin while the signal was between join and rejoin.
	if c, ok := s.first(start, func(e event) bool { return e.Name == "carplayd_connected" }); ok {
		if cross, ok := s.signalCrossing(start, "A", rejoinDBm, false); ok && c.T < cross.T {
			return m, fmt.Errorf("rejoined at %.2f before the signal reached the rejoin threshold (%.2f)", c.T, cross.T)
		}
	}
	return m, err
}

func twoCars(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s, "A", "B"); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range out && " + E + " wait box-disassoc 60 && " + E + " car B on && " + E + " wait box-assoc 60"); err != nil {
		return m, err
	}
	s.after(m, "carB_on→box_assoc", "car_on", "B", "box_assoc")
	if err := s.must(E + " phone B join && " + E + " wait phone-assoc B 30 && " + E + " wait phone-route B 60"); err != nil {
		return m, err
	}
	s.between(m, "phoneB_assoc→phone_route", "phone_assoc", "phone_route", "B")
	s.pull()
	// Car B's network must carry car B's own prefix.
	if r, ok := s.last("phone_route", "B"); ok && len(r.Args) > 1 && !strings.HasPrefix(r.Args[1], "fd5c:ba1e:f32:b:") {
		return m, fmt.Errorf("phone B got %s, want fd5c:ba1e:f32:b::/64", r.Args[1])
	}
	return m, nil
}

func bothOn(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(s.boxCmd("A", "B") + " && sleep 2 && " + E + " car B on && " + E + " car A on && " + E + " wait box-assoc 60"); err != nil {
		return m, err
	}
	// Whichever beacon came first, priority (A) should win within the grace period.
	s.must("sleep 8")
	s.pull()
	c, ok := s.last("carplayd_serving")
	if !ok || len(c.Args) == 0 {
		return m, fmt.Errorf("no carplayd_serving event")
	}
	m["serving_A_after_8s"] = 0
	if c.Args[0] == "A" {
		m["serving_A_after_8s"] = 1
	}
	if first, ok := s.first(0, func(e event) bool { return e.Name == "carplayd_serving" }); ok && first.Args[0] == "B" {
		if sw, ok := s.first(0, func(e event) bool { return e.Name == "carplayd_switch" }); ok {
			m["preempt_B→A"] = sw.T - first.T
		}
	}
	if m["serving_A_after_8s"] != 1 {
		return m, fmt.Errorf("still serving %s after 8 s; A has priority", c.Args[0])
	}
	s.after(m, "both_on→box_assoc", "car_on", "A", "box_assoc")
	return m, nil
}

// targets from docs/fast-connect-plan.md (seconds); metrics without one are informational.
var targets = map[string]float64{
	"car_on→box_assoc":            1.5,
	"carB_on→box_assoc":           1.5,
	"box_assoc→phone_route":       4,
	"out_of_range→phone_noroute":  21, // abrupt loss: no goodbye can reach the phone; bounded by the 20 s router lifetime
	"below_leave→phone_noroute":   5,  // leave_for (3 s) + goodbye
	"back_in_range→box_assoc":     1.5,
	"above_rejoin→box_assoc":      2.5,
	"upstream_down→phone_noroute": 10, // health every 3 s, 2 failures, 2 s connect timeout
	"upstream_up→phone_route":     4,
	"leaks":                       0,
	"mcast_spacing_violations":    0,
	"incomplete_withdrawals":      0,
	"rs_unanswered":               0,
	"rs_answer_max_s":             0.6,
}

func target(metric string) (float64, bool) {
	base := metric
	if i := strings.Index(metric, "#"); i > 0 {
		base = metric[:i]
	}
	t, ok := targets[base]
	return t, ok
}

func stats(v []float64) (min, p50, p90, max float64) {
	sort.Float64s(v)
	pick := func(q float64) float64 { return v[int(q*float64(len(v)-1)+0.5)] }
	return v[0], pick(0.5), pick(0.9), v[len(v)-1]
}

func main() {
	which := flag.String("scenarios", "all", "comma-separated scenario names, or all")
	n := flag.Int("n", 3, "repetitions per scenario (fresh VM each)")
	box := flag.String("box", "stock", "box firmware mode: stock (netifd/odhcpd + openwrt-carplay.sh) or carplayd")
	repo := flag.String("repo", ".", "repo root (contains emu/run.sh)")
	list := flag.Bool("list", false, "list scenarios")
	flag.Parse()
	if *list {
		for _, sc := range scenarios {
			fmt.Printf("%-16s [%s] %s\n", sc.name, sc.boxes, sc.about)
		}
		return
	}
	if *box != "stock" && *box != "carplayd" {
		fmt.Fprintln(os.Stderr, "-box must be stock or carplayd")
		os.Exit(2)
	}
	want := map[string]bool{}
	for _, w := range strings.Split(*which, ",") {
		want[strings.TrimSpace(w)] = true
	}

	stamp := time.Now().Format("20060102-150405") + "-" + *box
	outDir := filepath.Join(*repo, "emu/out/results")
	os.MkdirAll(outDir, 0o755)
	logf, _ := os.Create(filepath.Join(outDir, stamp+".log"))
	defer logf.Close()

	var runs []run
	for _, sc := range scenarios {
		if (!want["all"] && !want[sc.name]) || !strings.Contains(sc.boxes, *box) {
			continue
		}
		for rep := 1; rep <= *n; rep++ {
			fmt.Fprintf(os.Stderr, "[%s] %s #%d ... ", *box, sc.name, rep)
			fmt.Fprintf(logf, "\n===== [%s] %s #%d =====\n", *box, sc.name, rep)
			r := run{Box: *box, Scenario: sc.name, Rep: rep, Metrics: map[string]float64{}}
			s := &session{repo: *repo, box: *box, log: logf}
			if out, err := exec.Command(filepath.Join(*repo, "emu/run.sh"), "boot").CombinedOutput(); err != nil {
				r.Err = "boot: " + strings.TrimSpace(string(out))
			} else if err := s.must(E + " setup"); err != nil {
				r.Err = err.Error()
			} else {
				m, err := sc.fn(s)
				r.Metrics = m
				if err != nil {
					r.Err = err.Error()
				}
			}
			s.pull()
			if s.box == "carplayd" {
				// log only: its EV lines were already pulled from carplayd.events
				out, _ := exec.Command(filepath.Join(s.repo, "emu/run.sh"), "ssh", "tail -5 /tmp/emu/carplayd.log").CombinedOutput()
				fmt.Fprintf(s.log, "$ tail -5 carplayd.log\n%s", out)
			}
			r.Events = s.events
			runs = append(runs, r)
			if r.Err != "" {
				fmt.Fprintf(os.Stderr, "ERROR %s\n", r.Err)
			} else {
				fmt.Fprintf(os.Stderr, "ok %v\n", fmtMetrics(r.Metrics))
			}
		}
	}
	exec.Command(filepath.Join(*repo, "emu/run.sh"), "stop").Run()

	js, _ := json.MarshalIndent(runs, "", "  ")
	os.WriteFile(filepath.Join(outDir, stamp+".json"), js, 0o644)
	md := report(*box, runs)
	os.WriteFile(filepath.Join(outDir, stamp+".md"), []byte(md), 0o644)
	fmt.Print(md)
}

func fmtMetrics(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%.2f ", k, m[k])
	}
	return strings.TrimSpace(b.String())
}

func report(box string, runs []run) string {
	type key struct{ sc, metric string }
	vals := map[key][]float64{}
	errs := map[string]int{}
	total := map[string]int{}
	var order []key
	for _, r := range runs {
		total[r.Scenario]++
		if r.Err != "" {
			errs[r.Scenario]++
		}
		for m, v := range r.Metrics {
			base := m
			if i := strings.Index(m, "#"); i > 0 {
				base = m[:i]
			}
			k := key{r.Scenario, base}
			if _, seen := vals[k]; !seen {
				order = append(order, k)
			}
			vals[k] = append(vals[k], v)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].sc != order[j].sc {
			return order[i].sc < order[j].sc
		}
		return order[i].metric < order[j].metric
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Box: **%s**\n\n| Scenario | Metric (s) | n | min | median | p90 | max | target | |\n|---|---|---|---|---|---|---|---|---|\n", box)
	for _, k := range order {
		v := vals[k]
		mn, p50, p90, mx := stats(v)
		tgt, verdict := "", ""
		if t, ok := target(k.metric); ok {
			tgt = fmt.Sprintf("≤ %.1f", t)
			verdict = "✅"
			if p90 > t {
				verdict = "❌"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %.2f | %.2f | %.2f | %.2f | %s | %s |\n", k.sc, k.metric, len(v), mn, p50, p90, mx, tgt, verdict)
	}
	b.WriteString("\n")
	var scs []string
	for sc := range total {
		scs = append(scs, sc)
	}
	sort.Strings(scs)
	for _, sc := range scs {
		fmt.Fprintf(&b, "- %s: %d/%d runs ok\n", sc, total[sc]-errs[sc], total[sc])
	}
	return b.String()
}

// after records metric = first `to` event after the last `from` event for car.
func (s *session) after(m map[string]float64, metric, from, car, to string) {
	var a event
	var ok bool
	if car == "" {
		a, ok = s.last(from)
	} else {
		a, ok = s.last(from, car)
	}
	if !ok {
		return
	}
	if b, ok := s.first(a.T, func(e event) bool { return e.Name == to }); ok {
		m[metric] = b.T - a.T
	}
}

// jitter waits a random 0-1 s so car starts don't line up with the box's
// scan cycle (the harness is otherwise too regular).
func jitter() { time.Sleep(time.Duration(rand.Int63n(int64(time.Second)))) }

func count(s *session, name string) int {
	n := 0
	for _, e := range s.events {
		if e.Name == name {
			n++
		}
	}
	return n
}

func flap(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	// Alternate just above and just below the leave threshold (-84) for 30 s.
	var vals []string
	for i := 0; i < 15; i++ {
		vals = append(vals, "-82", "-86")
	}
	err := s.must(fmt.Sprintf("for v in %s; do %s car A signal $v; sleep 1; done", strings.Join(vals, " "), E))
	s.pull()
	m["leaves"] = float64(count(s, "carplayd_leave"))
	m["joins_after_first"] = float64(count(s, "carplayd_connected") - 1)
	// -82 is above leave (-84) but below rejoin (-76): once left, it must stay left.
	if m["joins_after_first"] > 0 {
		return m, fmt.Errorf("rejoined %v times while the signal never reached the rejoin threshold", m["joins_after_first"])
	}
	return m, err
}

func wpaKill(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	if err := s.must(E + " wpa-kill && " + E + " wait box-disassoc 30 && " + E + " wait box-assoc 60"); err != nil {
		return m, err
	}
	s.after(m, "wpa_killed→box_assoc", "wpa_killed", "", "box_assoc")
	s.pull()
	m["wpa_restarts"] = float64(count(s, "carplayd_wpa_restart"))
	if m["wpa_restarts"] < 1 {
		return m, fmt.Errorf("carplayd did not report restarting wpa_supplicant")
	}
	return m, s.must(E + " wait phone-route A 30")
}

func restart(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s); err != nil {
		return m, err
	}
	if err := s.must(E + " carplayd-restart"); err != nil {
		return m, err
	}
	// Its old wpa_supplicant keeps the association, so wait for the NEW carplayd
	// to say it serves (it restarts wpa_supplicant and re-joins first).
	started, _ := s.last("carplayd_started")
	for i := 0; i < 60; i++ {
		s.pull()
		if _, ok := s.first(started.T, func(e event) bool { return e.Name == "carplayd_serving" }); ok {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.after(m, "carplayd_started→serving", "carplayd_started", "", "carplayd_serving")
	if _, ok := m["carplayd_started→serving"]; !ok {
		return m, fmt.Errorf("restarted carplayd did not serve within 30 s")
	}
	if c, ok := s.last("carplayd_serving"); !ok || len(c.Args) < 2 || c.Args[1] != "fd5c:ba1e:f32:a::/64" {
		return m, fmt.Errorf("not serving car A's prefix after restart: %+v", c)
	}
	return m, s.must(E + " wait phone-route A 30")
}

func soak(s *session) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(s.boxCmd("A") + " && sleep 2"); err != nil {
		return m, err
	}
	for i := 1; i <= 20; i++ {
		jitter()
		if err := s.must(E + " car A on && " + E + " wait box-assoc 30"); err != nil {
			return m, fmt.Errorf("cycle %d: %v", i, err)
		}
		s.between(m, fmt.Sprintf("car_on→box_assoc#%d", i), "car_on", "box_assoc")
		if err := s.must(E + " car A off && " + E + " wait box-disassoc 30 && sleep 2"); err != nil {
			return m, fmt.Errorf("cycle %d: %v", i, err)
		}
	}
	return m, nil
}
