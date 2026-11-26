package main

import "testing"

func ch11() []Car { return []Car{{ID: "A", Channel: 11}} }

func TestNoSiblingMeansNoBackoff(t *testing.T) {
	s := newServeState()
	for tick := 1; tick <= 20; tick++ {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick, cochannelBackoff); skip {
			t.Fatalf("tick %d deferred with nobody serving", tick)
		}
	}
}

func TestSiblingOnADifferentChannelDoesNotDefer(t *testing.T) {
	s := newServeState()
	s.serving("cara", 1)
	for tick := 1; tick <= 20; tick++ {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick, cochannelBackoff); skip {
			t.Fatalf("tick %d deferred for a sibling on channel 1", tick)
		}
	}
}

func TestSiblingOnTheSameChannelBacksOffButNeverStops(t *testing.T) {
	s := newServeState()
	s.serving("cara", 11)
	scans := 0
	for tick := 1; tick <= 80; tick++ {
		sta, c, skip := deferHunt(s, "carb", ch11(), tick, cochannelBackoff)
		if skip {
			if sta != "cara" || c != 11 {
				t.Fatalf("tick %d: want the sibling named, got %q/%d", tick, sta, c)
			}
			continue
		}
		scans++
	}
	// 80 ticks, every 8th gets through: a second car is still discoverable.
	if scans != 10 {
		t.Fatalf("got %d scans in 80 ticks, want 10 (one per %d)", scans, cochannelBackoff)
	}
	if scans == 0 {
		t.Fatal("backoff became a halt, so a second car would never be found")
	}
}

func TestOwnServingDoesNotDeferItself(t *testing.T) {
	// hunt() already returns early when this link serves, but the predicate
	// must not consider a link its own sibling either.
	s := newServeState()
	s.serving("carb", 11)
	if _, _, skip := deferHunt(s, "carb", ch11(), 3, cochannelBackoff); skip {
		t.Fatal("a link deferred to itself")
	}
}

func TestChannelZeroNeverMatches(t *testing.T) {
	// channel 0 means "sweep all of 2.4 GHz", which cannot be pinned to one
	// neighbour's channel, so it must not trigger a backoff.
	s := newServeState()
	s.serving("cara", 11)
	if _, _, skip := deferHunt(s, "carb", []Car{{ID: "B", Channel: 0}}, 3, cochannelBackoff); skip {
		t.Fatal("a full sweep was deferred")
	}
	// and a serving channel of 0 is not recorded at all
	s2 := newServeState()
	s2.serving("cara", 0)
	if _, _, ok := s2.otherOn("carb", []int{0}); ok {
		t.Fatal("channel 0 was recorded as a serving channel")
	}
}

func TestStoppedClearsTheBackoff(t *testing.T) {
	s := newServeState()
	s.serving("cara", 11)
	if _, _, skip := deferHunt(s, "carb", ch11(), 3, cochannelBackoff); !skip {
		t.Fatal("expected a backoff while the sibling serves")
	}
	s.stopped("cara")
	for tick := 1; tick <= 20; tick++ {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick, cochannelBackoff); skip {
			t.Fatalf("tick %d still deferred after the sibling left", tick)
		}
	}
}

func TestNilServeStateIsSafe(t *testing.T) {
	// Tests build daemons directly; a nil shared state must not panic or
	// silently disable hunting.
	var s *serveState
	s.serving("cara", 11) // must not panic
	s.stopped("cara")
	if _, _, skip := deferHunt(s, "carb", ch11(), 3, cochannelBackoff); skip {
		t.Fatal("a nil serveState must never defer a hunt")
	}
}

func TestMultipleCarsOnOneRadio(t *testing.T) {
	// A shared radio hunting for two cars defers if a sibling is on either
	// channel.
	s := newServeState()
	s.serving("cara", 6)
	cars := []Car{{ID: "B", Channel: 11}, {ID: "C", Channel: 6}}
	sta, c, skip := deferHunt(s, "carb", cars, 3, cochannelBackoff)
	if !skip || sta != "cara" || c != 6 {
		t.Fatalf("want a backoff naming cara/6, got %q/%d skip=%v", sta, c, skip)
	}
}

// The backoff is configurable because it is the only lever the box has on the
// head unit's beacon-loss drops, and testing it must not need a rebuild.
func TestBackoffTicksFromConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  int
		want int
	}{
		{"unset falls back to the default", 0, cochannelBackoff},
		{"1 disables the backoff", 1, 1},
		{"a larger value is honoured", 30, 30},
		{"negative is treated as unset, never as 'never hunt'", -5, cochannelBackoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backoffTicks(Config{CochannelBackoff: tc.set}); got != tc.want {
				t.Errorf("backoffTicks(%d) = %d, want %d", tc.set, got, tc.want)
			}
		})
	}
}

// With the backoff at 1 the idle radio hunts every tick, which is the control
// arm of the experiment this knob exists for.
func TestBackoffOneNeverSkips(t *testing.T) {
	s := newServeState()
	s.serving("cara", 11)
	for tick := range 20 {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick, 1); skip {
			t.Fatalf("tick %d skipped with backoff=1; the control arm must always hunt", tick)
		}
	}
}

// A raised backoff must still let the second car be discovered -- never
// discovering it is the one outcome this must not produce.
func TestRaisedBackoffStillHunts(t *testing.T) {
	s := newServeState()
	s.serving("cara", 11)
	hunted := 0
	for tick := range 120 {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick, 30); !skip {
			hunted++
		}
	}
	if hunted != 4 {
		t.Errorf("hunted %d times in 120 ticks at backoff=30, want 4", hunted)
	}
}
