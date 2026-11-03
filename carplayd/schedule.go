package main

import (
	"math/rand"
	"time"
)

// RFC 4861 timing (sections 6.2.1, 6.2.4, 6.2.6, 10).
const (
	minAllowedRtrAdvInterval = 3 * time.Second        // smallest MinRtrAdvInterval allowed
	minAllowedMaxRtrAdv      = 4 * time.Second        // smallest MaxRtrAdvInterval allowed
	minDelayBetweenRAs       = 3 * time.Second        // MIN_DELAY_BETWEEN_RAS: every multicast RA
	maxRADelay               = 500 * time.Millisecond // MAX_RA_DELAY_TIME: every solicited RA
)

// minRtrAdv is the default MinRtrAdvInterval for a MaxRtrAdvInterval:
// 0.75 × Max below 9 s, 0.33 × Max from 9 s, never under 3 s.
func minRtrAdv(max time.Duration) time.Duration {
	m := max / 3
	if max < 9*time.Second {
		m = max * 3 / 4
	}
	if m < minAllowedRtrAdvInterval {
		m = minAllowedRtrAdvInterval
	}
	return m
}

// nextUnsolicited is a uniformly random interval in [min, max]: periodic
// multicast RAs must not go out at a fixed period (6.2.4).
func nextUnsolicited(r *rand.Rand, min, max time.Duration) time.Duration {
	return min + time.Duration(r.Int63n(int64(max-min)+1))
}

// solicitedDelay is the random 0..MAX_RA_DELAY_TIME wait before answering a
// Router Solicitation, required "in all cases" (6.2.6).
func solicitedDelay(r *rand.Rand) time.Duration {
	return time.Duration(r.Int63n(int64(maxRADelay) + 1))
}

// mcastAt is when a multicast RA wanted after `delay` may go out, given the
// last multicast RA: no sooner than MIN_DELAY_BETWEEN_RAS plus the delay
// after it (6.2.6).
func mcastAt(now, last time.Time, delay time.Duration) time.Time {
	if !last.IsZero() && now.Sub(last) < minDelayBetweenRAs {
		return last.Add(minDelayBetweenRAs + delay)
	}
	return now.Add(delay)
}

// health tracks upstream reachability with hysteresis: down after `fail`
// consecutive failed checks, up again after one success.
type health struct {
	ok    bool
	fails int
}

// update records one check and reports whether the state changed.
func (h *health) update(pass bool, fail int) (changed bool) {
	if pass {
		h.fails = 0
		if !h.ok {
			h.ok = true
			return true
		}
		return false
	}
	h.fails++
	if h.ok && h.fails >= fail {
		h.ok = false
		return true
	}
	return false
}
