package main

import "sync"

// Co-channel hunting: one radio's scanning disturbs another radio's link.
//
// With a radio per car and both cars configured on channel 11, the idle radio
// hunts on exactly the channel the busy radio is serving on, from an antenna
// inches away. A single-channel scan on this hardware takes 131-156 ms
// (docs/status.md) and the hunt runs every second, so roughly 7% of the time a
// co-located radio is transmitting probe requests and listening on the channel
// carrying someone's CarPlay session. That is airtime the serving link does not
// get, and receive desense it cannot measure: the box's own `tx_retries` and
// `tx_failed` are not even implemented by ath9k_htc, so this is invisible from
// the usual counters.
//
// The fix is a backoff and deliberately not a halt. Halting would mean a second
// car is never discovered while the first is served, which defeats the whole
// point of having a radio per car. At the default backoff the idle radio still
// sweeps, just every 8th tick instead of every tick -- so a second car is found
// within about 8 s rather than about 1 s, and the co-channel duty cycle drops
// by the same factor.
//
// UNMEASURED. This is a plausible contributor to the residual stutter reported
// on 2026-11-16 after the rate cap, not a demonstrated one. The test for it is
// cheap and needs a car present: compare round-trip jitter to the phone with
// the idle radio's supplicant running and stopped. Until that is run, treat the
// backoff as cost-free insurance rather than a fix.

// cochannelBackoff is the default number of hunt ticks to skip while a sibling
// radio is serving a car on the same channel. 8 trades about 7 s of extra
// discovery latency for the second car against an eightfold cut in
// interference with the first car's live session.
//
// Configurable as `cochannel_backoff` since 2026-11-25, because this is the
// only lever the box has on a problem that is otherwise entirely the head
// unit's, and testing it had meant a rebuild and a redeploy.
//
// What prompted it: on 2026-11-25 the F32 lost its link five times, every one
// preceded by CTRL-EVENT-BEACON-LOSS at -56..-62 dBm with tx_failed=0 and full
// 65 Mb/s rates -- the AP stopped beaconing while our link to it was perfect.
// One of those outages lasted nearly three minutes, with neither radio able to
// see the AP. That is the head unit's doing and no setting here changes it.
// But the co-channel hypothesis in this file is still the one untested way the
// box could be *contributing*: carb sweeps channel 11, from an antenna inches
// away, while cara serves a live CarPlay session on it. Raising this number
// shrinks that duty cycle.
//
// STILL UNMEASURED. Raising it is a cheap experiment, not a known fix. The way
// to judge it is to count beacon-loss events per car per day before and after:
//
//	journalctl -u carplayd --since '7 days ago' | grep -c 'cara CTRL-EVENT-BEACON-LOSS'
//
// For reference, the week to 2026-11-25: cara 5 beacon losses and 5 of them
// killed the link; carb 153 beacon losses and only 2 killed it. If the
// backoff matters, cara's count should fall. If it does not move, the blame is
// the head unit's alone and this should go back to 8.
const cochannelBackoff = 8

// backoffTicks is the configured backoff, or the default when unset. A value
// of 1 disables the backoff (every tick hunts); anything below 1 is treated as
// unset rather than as "never hunt", because never discovering the second car
// is the one outcome this must not produce by accident.
func backoffTicks(cfg Config) int {
	if cfg.CochannelBackoff > 1 {
		return cfg.CochannelBackoff
	}
	if cfg.CochannelBackoff == 1 {
		return 1
	}
	return cochannelBackoff
}

// serveState records which channel each radio is currently serving on. Every
// link in the process shares one, the same way they share the metrics registry
// and the learned profiles.
type serveState struct {
	mu sync.Mutex
	on map[string]int // STA name -> channel being served
}

func newServeState() *serveState { return &serveState{on: map[string]int{}} }

// serving marks a radio as carrying a live car link on ch.
func (s *serveState) serving(sta string, ch int) {
	if s == nil || sta == "" || ch == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on[sta] = ch
}

// stopped clears a radio.
func (s *serveState) stopped(sta string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.on, sta)
}

// otherOn reports whether a radio other than self is serving on any of chans,
// and which. A zero channel in chans never matches: channel 0 means "hunt all
// of 2.4 GHz", and a sweep cannot be pinned to one neighbour's channel.
func (s *serveState) otherOn(self string, chans []int) (string, int, bool) {
	if s == nil {
		return "", 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for sta, ch := range s.on {
		if sta == self {
			continue
		}
		for _, c := range chans {
			if c != 0 && c == ch {
				return sta, ch, true
			}
		}
	}
	return "", 0, false
}

// channels is the set of channels a link would hunt on.
func channels(cars []Car) []int {
	out := make([]int, 0, len(cars))
	for _, c := range cars {
		out = append(out, c.Channel)
	}
	return out
}

// deferHunt reports whether this tick's scan should be skipped because a
// sibling radio is serving on a channel we would scan. It also returns the
// sibling and channel so the decision can be logged rather than guessed at.
func deferHunt(s *serveState, self string, cars []Car, tick int, backoff int) (sta string, ch int, skip bool) {
	sta, ch, busy := s.otherOn(self, channels(cars))
	if !busy {
		return "", 0, false
	}
	if backoff < 1 {
		backoff = cochannelBackoff
	}
	// Let every backoff-th tick through so a second car is still discovered,
	// just more slowly.
	return sta, ch, tick%backoff != 0
}
