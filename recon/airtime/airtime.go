package main

import "math"

// Airtime, not bytes.
//
// The question this answers is "how much of the channel is left for us after
// CarPlay", and bytes cannot answer it. On this link the head unit's AP is
// 1x1 HT20, and a frame sent at MCS 0 occupies roughly ten times the airtime
// of the same frame at MCS 7. A station moving 20 kB/s at a low rate can cost
// more of the channel than one moving 100 kB/s at a high rate. So every frame
// is converted to the microseconds it actually held the medium.
//
// All of this is IEEE 802.11-2020 timing for 2.4 GHz. Numbers are not guessed:
// the per-MCS data bits per symbol are the standard's HT20 1-spatial-stream
// values, and the preamble lengths are the mixed-format ones these cars use
// (the beacons are HT, the legacy rates are OFDM/CCK).

// htNDBPS is data bits per OFDM symbol, HT20, one spatial stream, MCS 0-7.
var htNDBPS = [8]int{26, 52, 78, 104, 156, 208, 234, 260}

const (
	// Mixed-format HT preamble: L-STF 8 + L-LTF 8 + L-SIG 4 + HT-SIG 8 +
	// HT-STF 4 + HT-LTF 4 (one per spatial stream) = 36 us.
	htPreambleUS = 36.0
	// OFDM (11g): 16 us preamble + 4 us SIGNAL.
	ofdmPreambleUS = 20.0
	// CCK (11b) long and short PLCP.
	cckLongPreambleUS  = 192.0
	cckShortPreambleUS = 96.0

	symbolUS    = 4.0
	symbolSGIUS = 3.6

	// Service field (16) + tail (6) are carried in the data symbols.
	serviceTailBits = 22

	// SIFS in 2.4 GHz, and a block ACK/ACK sent at a basic rate. Counted
	// separately from the frame so the two can be reported apart: a channel
	// full of small frames loses far more to overhead than one moving bulk.
	sifsUS = 10.0
	ackUS  = 28.0 // 14-byte ACK at 6 Mbit/s OFDM, rounded
)

// rateInfo is what radiotap told us about how a frame was sent.
type rateInfo struct {
	HT      bool
	MCS     int // valid when HT
	ShortGI bool
	Rate500 int  // legacy rate in 500 kbit/s units, valid when !HT
	CCK     bool // legacy and below 6 Mbit/s
}

// Mbps returns the nominal PHY rate, for reporting.
func (r rateInfo) Mbps() float64 {
	if r.HT {
		if r.MCS < 0 || r.MCS > 7 {
			return 0
		}
		sym := symbolUS
		if r.ShortGI {
			sym = symbolSGIUS
		}
		return float64(htNDBPS[r.MCS]) / sym
	}
	return float64(r.Rate500) / 2
}

// frameUS is the microseconds a frame of n octets occupied the medium,
// excluding SIFS and the ACK. Returns 0 if the rate is unusable, so a caller
// can count the frame as unaccounted rather than silently charging it zero.
func frameUS(n int, r rateInfo) float64 {
	if n <= 0 {
		return 0
	}
	if r.HT {
		if r.MCS < 0 || r.MCS > 7 {
			return 0
		}
		sym := symbolUS
		if r.ShortGI {
			sym = symbolSGIUS
		}
		nsym := math.Ceil(float64(serviceTailBits+8*n) / float64(htNDBPS[r.MCS]))
		return htPreambleUS + nsym*sym
	}
	if r.Rate500 <= 0 {
		return 0
	}
	mbps := float64(r.Rate500) / 2
	if r.CCK || mbps < 6 {
		// 11b: the payload is sent at the data rate, bit by bit.
		return cckLongPreambleUS + math.Ceil(float64(8*n)/mbps)
	}
	// 11g OFDM: NDBPS is 4 bits per MHz-symbol, i.e. rate * 4.
	ndbps := mbps * symbolUS
	nsym := math.Ceil(float64(serviceTailBits+8*n) / ndbps)
	return ofdmPreambleUS + nsym*symbolUS
}

// frameCostUS adds the medium time a unicast data frame really costs: the
// frame, a SIFS, and the ACK. Broadcast and management frames that are not
// acknowledged pass isUnicast=false.
//
// Deliberately NOT included: DIFS and the random backoff before the frame.
// Those are contention, not occupancy -- counting them would make a quiet
// channel look busy. So this is a floor on utilisation, and the report says so.
func frameCostUS(n int, r rateInfo, isUnicast bool) float64 {
	d := frameUS(n, r)
	if d == 0 {
		return 0
	}
	if isUnicast {
		d += sifsUS + ackUS
	}
	return d
}
