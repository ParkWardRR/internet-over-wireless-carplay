// emuharness runs the garage scenarios against the emulated box firmware on
// build-host and reports how long each step takes.
//
//	emuharness -scenarios all -n 5            # every scenario, 5 fresh VMs each
//	emuharness -scenarios detect,drive-out -n 10
//
// Each repetition boots a fresh VM (emu/run.sh boot), builds the topology
// (emu.sh setup), runs the scenario's steps over SSH and collects the
// "EV <t> <event> ..." lines. Metrics are differences between events; the
// report gives min/median/p90/max per metric and checks them against targets.
// Results go to emu/out/results/<stamp>.{json,md}.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
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
	Scenario string             `json:"scenario"`
	Rep      int                `json:"rep"`
	Events   []event            `json:"events"`
	Metrics  map[string]float64 `json:"metrics"`
	Err      string             `json:"error,omitempty"`
}

type session struct {
	repo   string
	events []event
	log    *os.File
}

// sh runs a command in the VM and collects its EV lines.
func (s *session) sh(cmd string) (string, error) {
	c := exec.Command(filepath.Join(s.repo, "emu/run.sh"), "ssh", cmd)
	out, err := c.CombinedOutput()
	fmt.Fprintf(s.log, "$ %s\n%s", cmd, out)
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && f[0] == "EV" {
			if t, perr := strconv.ParseFloat(f[1], 64); perr == nil {
				s.events = append(s.events, event{T: t, Name: f[2], Args: f[3:]})
			}
		}
	}
	return string(out), err
}

// must runs steps that have to succeed for the scenario to mean anything.
func (s *session) must(cmd string) error {
	if _, err := s.sh(cmd); err != nil {
		return fmt.Errorf("%q: %v", cmd, err)
	}
	return nil
}

// last returns the time of the last event with this name (and first arg, if given).
func (s *session) last(name string, arg ...string) (float64, bool) {
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Name == name && (len(arg) == 0 || (len(e.Args) > 0 && e.Args[0] == arg[0])) {
			return e.T, true
		}
	}
	return 0, false
}

// between records metric = t(to) - t(from) using the most recent of each.
func (s *session) between(m map[string]float64, metric, from, to string, arg ...string) {
	a, ok1 := s.last(from, arg...)
	b, ok2 := s.last(to, arg...)
	if ok1 && ok2 && b >= a {
		m[metric] = b - a
	}
}

const E = "/root/carplay/emu.sh"

// Scenarios. Each assumes a fresh VM with `emu.sh setup` done.
var scenarios = []struct {
	name, about string
	fn          func(s *session, env string) (map[string]float64, error)
}{
	{"detect", "box configured and hunting, car A starts: how fast does the box join, phone get a route, internet work", detect},
	{"detect-scanlist", "same with scan_list limited to the car's channel (2462)", detectScanList},
	{"box-late", "car on and phone joined first (RS burst over), then the box joins: unsolicited RA timing", boxLate},
	{"drive-out", "car A leaves the box's range (phone stays in the cabin): box loses it, phone loses the route", driveOut},
	{"return", "car A comes back into range after a drive-out: rejoin and route", returnScenario},
	{"cycle", "car A off/on 5 times with the box hunting: detection time per cycle", cycle},
}

func up(s *session, env string) error {
	if err := s.must(env + E + " car A on && " + env + E + " box-apply A && " + E + " wait box-assoc 60"); err != nil {
		return err
	}
	if err := s.must(E + " phone A join && " + E + " wait phone-assoc A 30 && " + E + " wait phone-route A 60"); err != nil {
		return err
	}
	return nil
}

func detectWith(s *session, env string) (map[string]float64, error) {
	m := map[string]float64{}
	// Box first (as in production), car off: the box is hunting.
	if err := s.must(env + E + " box-apply A && sleep 3"); err != nil {
		return m, err
	}
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

func detect(s *session, _ string) (map[string]float64, error) { return detectWith(s, "") }
func detectScanList(s *session, _ string) (map[string]float64, error) {
	return detectWith(s, "SCAN_LIST=2462 ")
}

func boxLate(s *session, _ string) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(E + " car A on && " + E + " phone A join && " + E + " wait phone-assoc A 30"); err != nil {
		return m, err
	}
	// Let the phone's Router Solicitations run out (3 × 4 s) before the box appears.
	if err := s.must("sleep 15 && " + E + " box-apply A && " + E + " wait box-assoc 60 && " + E + " wait phone-route A 90"); err != nil {
		return m, err
	}
	s.between(m, "box_assoc→phone_route", "box_assoc", "phone_route")
	return m, nil
}

func driveOut(s *session, env string) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s, env); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range out && " + E + " wait box-disassoc 60 && " + E + " wait phone-noroute A 180"); err != nil {
		return m, err
	}
	s.between(m, "out_of_range→box_disassoc", "car_range", "box_disassoc", "A")
	s.between(m, "out_of_range→phone_noroute", "car_range", "phone_noroute", "A")
	return m, nil
}

func returnScenario(s *session, env string) (map[string]float64, error) {
	m := map[string]float64{}
	if err := up(s, env); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range out && " + E + " wait box-disassoc 60 && sleep 5"); err != nil {
		return m, err
	}
	if err := s.must(E + " car A range in && " + E + " wait box-assoc 60 && " + E + " wait phone-route A 120"); err != nil {
		return m, err
	}
	s.between(m, "back_in_range→box_assoc", "car_range", "box_assoc", "A")
	s.between(m, "box_assoc→phone_route", "box_assoc", "phone_route")
	return m, nil
}

func cycle(s *session, _ string) (map[string]float64, error) {
	m := map[string]float64{}
	if err := s.must(E + " box-apply A && sleep 2"); err != nil {
		return m, err
	}
	for i := 1; i <= 5; i++ {
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

// targets from docs/fast-connect-plan.md (seconds); metrics without one are informational.
var targets = map[string]float64{
	"car_on→box_assoc":           1.5,
	"box_assoc→phone_route":      4,
	"out_of_range→phone_noroute": 2,
	"back_in_range→box_assoc":    1.5,
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
	repo := flag.String("repo", ".", "repo root (contains emu/run.sh)")
	list := flag.Bool("list", false, "list scenarios")
	flag.Parse()
	if *list {
		for _, sc := range scenarios {
			fmt.Printf("%-16s %s\n", sc.name, sc.about)
		}
		return
	}
	want := map[string]bool{}
	for _, w := range strings.Split(*which, ",") {
		want[strings.TrimSpace(w)] = true
	}

	stamp := time.Now().Format("20060102-150405")
	outDir := filepath.Join(*repo, "emu/out/results")
	os.MkdirAll(outDir, 0o755)
	logf, _ := os.Create(filepath.Join(outDir, stamp+".log"))
	defer logf.Close()

	var runs []run
	for _, sc := range scenarios {
		if !want["all"] && !want[sc.name] {
			continue
		}
		for rep := 1; rep <= *n; rep++ {
			fmt.Fprintf(os.Stderr, "%s #%d ... ", sc.name, rep)
			fmt.Fprintf(logf, "\n===== %s #%d =====\n", sc.name, rep)
			r := run{Scenario: sc.name, Rep: rep, Metrics: map[string]float64{}}
			s := &session{repo: *repo, log: logf}
			boot := exec.Command(filepath.Join(*repo, "emu/run.sh"), "boot")
			if out, err := boot.CombinedOutput(); err != nil {
				r.Err = "boot: " + strings.TrimSpace(string(out))
			} else if err := s.must(E + " setup"); err != nil {
				r.Err = err.Error()
			} else {
				m, err := sc.fn(s, "")
				r.Metrics = m
				if err != nil {
					r.Err = err.Error()
				}
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
	md := report(runs)
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
		fmt.Fprintf(&b, "%s=%.2fs ", k, m[k])
	}
	return strings.TrimSpace(b.String())
}

func report(runs []run) string {
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
	fmt.Fprintf(&b, "| Scenario | Metric | n | min | median | p90 | max | target | |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, k := range order {
		v := vals[k]
		mn, p50, p90, mx := stats(v)
		tgt, verdict := "", ""
		if t, ok := target(k.metric); ok {
			tgt = fmt.Sprintf("≤ %.1f s", t)
			verdict = "✅"
			if p90 > t {
				verdict = "❌"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %.2f | %.2f | %.2f | %.2f | %s | %s |\n", k.sc, k.metric, len(v), mn, p50, p90, mx, tgt, verdict)
	}
	b.WriteString("\n")
	for sc, t := range total {
		if errs[sc] > 0 {
			fmt.Fprintf(&b, "- %s: %d of %d runs failed (see the .log)\n", sc, errs[sc], t)
		}
	}
	return b.String()
}
