package main

import "testing"

func ch11() []Car { return []Car{{ID: "A", Channel: 11}} }

func TestNoSiblingMeansNoBackoff(t *testing.T) {
	s := newServeState()
	for tick := 1; tick <= 20; tick++ {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick); skip {
			t.Fatalf("tick %d deferred with nobody serving", tick)
		}
	}
}

func TestSiblingOnADifferentChannelDoesNotDefer(t *testing.T) {
	s := newServeState()
	s.serving("cara", 1)
	for tick := 1; tick <= 20; tick++ {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick); skip {
			t.Fatalf("tick %d deferred for a sibling on channel 1", tick)
		}
	}
}

func TestSiblingOnTheSameChannelBacksOffButNeverStops(t *testing.T) {
	s := newServeState()
	s.serving("cara", 11)
	scans := 0
	for tick := 1; tick <= 80; tick++ {
		sta, c, skip := deferHunt(s, "carb", ch11(), tick)
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
	if _, _, skip := deferHunt(s, "carb", ch11(), 3); skip {
		t.Fatal("a link deferred to itself")
	}
}

func TestChannelZeroNeverMatches(t *testing.T) {
	// channel 0 means "sweep all of 2.4 GHz", which cannot be pinned to one
	// neighbour's channel, so it must not trigger a backoff.
	s := newServeState()
	s.serving("cara", 11)
	if _, _, skip := deferHunt(s, "carb", []Car{{ID: "B", Channel: 0}}, 3); skip {
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
	if _, _, skip := deferHunt(s, "carb", ch11(), 3); !skip {
		t.Fatal("expected a backoff while the sibling serves")
	}
	s.stopped("cara")
	for tick := 1; tick <= 20; tick++ {
		if _, _, skip := deferHunt(s, "carb", ch11(), tick); skip {
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
	if _, _, skip := deferHunt(s, "carb", ch11(), 3); skip {
		t.Fatal("a nil serveState must never defer a hunt")
	}
}

func TestMultipleCarsOnOneRadio(t *testing.T) {
	// A shared radio hunting for two cars defers if a sibling is on either
	// channel.
	s := newServeState()
	s.serving("cara", 6)
	cars := []Car{{ID: "B", Channel: 11}, {ID: "C", Channel: 6}}
	sta, c, skip := deferHunt(s, "carb", cars, 3)
	if !skip || sta != "cara" || c != 6 {
		t.Fatalf("want a backoff naming cara/6, got %q/%d skip=%v", sta, c, skip)
	}
}
