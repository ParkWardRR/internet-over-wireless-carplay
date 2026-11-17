package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// The report splits "CarPlay active" from "car away" itself.
//
// The first version of this analysis was done by hand, and the aggregate it
// produced was misleading: it averaged over 17 minutes during which the car was
// absent for 6, diluting every figure. Worse, the headline number was simply
// wrong, because the busiest station was misclassified (see role()). Both
// mistakes are the kind that look like results, so the split and the
// classification now live in the tool where they can be tested.

type agg struct {
	MAC     string
	Role    string
	AirUS   float64
	Bytes   int
	Frames  int
	Retries int
	ToHU    int
	SigSum  int
	SigN    int
}

func pctl(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// reportFile prints the analysis for one run file.
func reportFile(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var secs []*second
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var s second
		if err := json.Unmarshal(line, &s); err != nil || s.T == "" {
			continue // a stall record or a truncated final line
		}
		secs = append(secs, &s)
	}
	if len(secs) == 0 {
		return fmt.Errorf("%s: no usable seconds", path)
	}

	// Re-derive phone-vs-other from the data rather than trusting the stored
	// label.
	//
	// The capture-time fix only helps future runs: a file written by the old
	// binary has the wrong role baked into every second, and the one real
	// dataset this project has -- the 2026-11-16 drive -- is such a file. The
	// bytes needed to decide are right there in it, so the report recomputes
	// instead of inheriting a known-bad answer.
	//
	// "hu" and "box" are kept as stored: those come from a direct MAC match
	// against the configured lists and were never order-dependent. Only
	// phone-vs-other was.
	for _, s := range secs {
		for _, st := range s.Stations {
			if st.Role == "hu" || st.Role == "box" {
				continue
			}
			if st.Bytes > 0 && float64(st.ToHU)/float64(st.Bytes) > 0.5 {
				st.Role = "phone"
			} else {
				st.Role = "other"
			}
		}
	}

	// A second counts as CarPlay-active if any station classified as a phone
	// actually spent airtime in it. That is the behaviour-based role, so it no
	// longer depends on which frame arrived first.
	var live, away []*second
	for _, s := range secs {
		active := false
		for _, st := range s.Stations {
			if st.Role == "phone" && st.AirUS > 0 {
				active = true
				break
			}
		}
		if active {
			live = append(live, s)
		} else {
			away = append(away, s)
		}
	}

	fmt.Fprintf(w, "== %s ==\n", path)
	fmt.Fprintf(w, "seconds %d  (%s .. %s)\n", len(secs), secs[0].T, secs[len(secs)-1].T)
	fmt.Fprintf(w, "CarPlay active %d s, car away %d s\n", len(live), len(away))

	busyOf := func(ss []*second) []float64 {
		out := make([]float64, 0, len(ss))
		for _, s := range ss {
			out = append(out, s.BusyPct)
		}
		return out
	}

	if len(away) > 0 {
		b := busyOf(away)
		fmt.Fprintf(w, "\ncar away      channel busy: median %.1f%%  p95 %.1f%%  max %.1f%%   (this is the neighbourhood)\n",
			pctl(b, .5), pctl(b, .95), pctl(b, 1))
	}
	if len(live) == 0 {
		fmt.Fprintln(w, "\nNo CarPlay-active seconds: nothing to say about what it costs.")
		return nil
	}

	b := busyOf(live)
	fmt.Fprintf(w, "CarPlay active channel busy: median %.1f%%  p95 %.1f%%  max %.1f%%\n",
		pctl(b, .5), pctl(b, .95), pctl(b, 1))

	// What CarPlay itself costs, per second: the phones plus the head units.
	var cpAir, phAir, phBytes, free, headroom []float64
	for _, s := range live {
		var cp, ph float64
		var phb int
		for _, st := range s.Stations {
			switch st.Role {
			case "phone":
				cp += st.AirUS
				ph += st.AirUS
				phb += st.Bytes
			case "hu":
				cp += st.AirUS
			}
		}
		cpAir = append(cpAir, cp/1e4) // us -> % of a second
		phAir = append(phAir, ph/1e4)
		phBytes = append(phBytes, float64(phb))
		fr := 100 - s.BusyPct
		free = append(free, fr)
		headroom = append(headroom, s.BestMbps*fr/100)
	}

	fmt.Fprintf(w, "\nCarPlay's own cost, while active\n")
	fmt.Fprintf(w, "  phone -> head unit airtime : median %.1f%%  p95 %.1f%%  max %.1f%% of the channel\n",
		pctl(phAir, .5), pctl(phAir, .95), pctl(phAir, 1))
	fmt.Fprintf(w, "  phone -> head unit bytes   : median %.0f kB/s  p95 %.0f  max %.0f\n",
		pctl(phBytes, .5)/1e3, pctl(phBytes, .95)/1e3, pctl(phBytes, 1)/1e3)
	fmt.Fprintf(w, "  CarPlay total (phone + HU) : median %.1f%%  p95 %.1f%%  max %.1f%% of the channel\n",
		pctl(cpAir, .5), pctl(cpAir, .95), pctl(cpAir, 1))

	fmt.Fprintf(w, "\nWhat is left for us\n")
	fmt.Fprintf(w, "  channel idle               : median %.1f%%  p5 %.1f%%\n", pctl(free, .5), pctl(free, .05))
	fmt.Fprintf(w, "  headroom at the best rate  : median %.1f Mbit/s  p5 %.1f\n", pctl(headroom, .5), pctl(headroom, .05))
	fmt.Fprintf(w, "  halved for the relay       : median %.1f Mbit/s  p5 %.1f\n", pctl(headroom, .5)/2, pctl(headroom, .05)/2)

	// Per-station totals over the whole file, by airtime.
	tot := map[string]*agg{}
	var allAir float64
	for _, s := range secs {
		for m, st := range s.Stations {
			a := tot[m]
			if a == nil {
				a = &agg{MAC: m}
				tot[m] = a
			}
			// NOT a.Role = st.Role: that takes the label from whichever
			// second happens to come last, so one quiet second in which a
			// phone sent little relabels the whole station. The role is
			// derived from the totals below, after everything is summed --
			// the same mistake as classifying on the first frame, one level up.
			if st.Role == "hu" || st.Role == "box" {
				a.Role = st.Role
			}
			a.AirUS += st.AirUS
			a.Bytes += st.Bytes
			a.Frames += st.Frames
			a.Retries += st.Retries
			a.ToHU += st.ToHU
			a.SigSum += st.Sig
			a.SigN++
			allAir += st.AirUS
		}
	}
	list := make([]*agg, 0, len(tot))
	for _, a := range tot {
		if a.Role == "" {
			if a.Bytes > 0 && float64(a.ToHU)/float64(a.Bytes) > 0.5 {
				a.Role = "phone"
			} else {
				a.Role = "other"
			}
		}
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].AirUS > list[j].AirUS })
	if allAir == 0 {
		allAir = 1
	}
	fmt.Fprintf(w, "\n%-18s %-6s %7s %10s %9s %6s\n", "station", "role", "air%", "B/s", "retries", "sig")
	for i, a := range list {
		if i >= 8 || a.AirUS == 0 {
			break
		}
		sig := 0
		if a.SigN > 0 {
			sig = a.SigSum / a.SigN
		}
		fmt.Fprintf(w, "%-18s %-6s %6.1f%% %10.0f %8.1f%% %6d\n",
			a.MAC, a.Role, 100*a.AirUS/allAir, float64(a.Bytes)/float64(len(secs)),
			100*float64(a.Retries)/math.Max(float64(a.Frames), 1), sig)
	}

	fmt.Fprintln(w, "\nbusy_pct charges occupancy only -- no DIFS, backoff or contention -- so it")
	fmt.Fprintln(w, "is a FLOOR and headroom is OPTIMISTIC. Our traffic is relayed by the head")
	fmt.Fprintln(w, "unit, so it crosses the air twice: halve the headroom again.")
	fmt.Fprintln(w, "\nAnd airtime is not the binding constraint. Measured 2026-11-16, the head")
	fmt.Fprintln(w, "unit's relay collapsed at about 3.8 Mbit/s of our traffic -- 60-87% loss,")
	fmt.Fprintln(w, "2051 ms round trips, CarPlay dropped -- with airtime still largely free.")
	fmt.Fprintln(w, "Plenty of headroom here does NOT license a higher cap.")
	return nil
}
