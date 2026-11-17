package main

import "encoding/binary"

// Radiotap, far enough in to reach the MCS field.
//
// The existing decoder in recon/carrecon stops at field 15, which is enough for
// signal and legacy rate. Airtime needs field 19 (MCS), so the table runs to
// 19 and the alignment has to be right for every field in between -- a wrong
// size anywhere before 19 silently shifts the MCS read and every airtime
// number with it. That is what the tests pin.

// rtField is {alignment, size} in presence-bit order, IEEE radiotap.
var rtField = [...][2]int{
	{8, 8}, // 0  TSFT
	{1, 1}, // 1  Flags
	{1, 1}, // 2  Rate
	{2, 4}, // 3  Channel
	{2, 2}, // 4  FHSS
	{1, 1}, // 5  dBm antenna signal
	{1, 1}, // 6  dBm antenna noise
	{2, 2}, // 7  Lock quality
	{2, 2}, // 8  TX attenuation
	{2, 2}, // 9  dB TX attenuation
	{1, 1}, // 10 dBm TX power
	{1, 1}, // 11 Antenna
	{1, 1}, // 12 dB antenna signal
	{1, 1}, // 13 dB antenna noise
	{2, 2}, // 14 RX flags
	{2, 2}, // 15 TX flags
	{1, 1}, // 16 RTS retries
	{1, 1}, // 17 Data retries
	{4, 8}, // 18 XChannel
	{1, 3}, // 19 MCS: known, flags, index
}

const (
	rtFlagShortPreamble = 0x02
	rtFlagBadFCS        = 0x40

	mcsKnownBW    = 0x01
	mcsKnownIndex = 0x02
	mcsKnownGI    = 0x04
	mcsFlagSGI    = 0x04
	mcsFlagBW40   = 0x01 // bandwidth field 1 = 40 MHz
)

type radiotap struct {
	HeaderLen int
	Signal    int
	Rate      rateInfo
	BadFCS    bool
	OK        bool
}

// parseRadiotap reads the fields airtime needs. A frame whose rate cannot be
// determined comes back with Rate zeroed, and the caller counts it as
// unaccounted rather than charging it zero airtime.
func parseRadiotap(b []byte) radiotap {
	var rt radiotap
	if len(b) < 8 || b[0] != 0 {
		return rt
	}
	hlen := int(binary.LittleEndian.Uint16(b[2:4]))
	if hlen < 8 || hlen > len(b) {
		return rt
	}
	rt.HeaderLen, rt.OK = hlen, true

	// Presence words: bit 31 set means another word follows. Only the first
	// word's bits map to the field table; later words are vendor namespaces
	// we do not read, but they still consume header space before the fields.
	off := 4
	first := binary.LittleEndian.Uint32(b[off:])
	present := first
	for present&(1<<31) != 0 {
		off += 4
		if off+4 > hlen {
			return rt
		}
		present = binary.LittleEndian.Uint32(b[off:])
	}
	pos := off + 4

	var flags byte
	for i := range rtField {
		if first&(1<<uint(i)) == 0 {
			continue
		}
		align, size := rtField[i][0], rtField[i][1]
		if r := pos % align; r != 0 {
			pos += align - r
		}
		if pos+size > hlen {
			break
		}
		switch i {
		case 1:
			flags = b[pos]
			rt.BadFCS = flags&rtFlagBadFCS != 0
		case 2:
			rt.Rate.Rate500 = int(b[pos])
		case 5:
			rt.Signal = int(int8(b[pos]))
		case 19:
			known, mflags, idx := b[pos], b[pos+1], b[pos+2]
			if known&mcsKnownIndex != 0 {
				rt.Rate.HT = true
				rt.Rate.MCS = int(idx)
				if known&mcsKnownGI != 0 {
					rt.Rate.ShortGI = mflags&mcsFlagSGI != 0
				}
			}
		}
		pos += size
	}
	// Legacy below 6 Mbit/s is CCK (11b); the short-preamble flag matters there.
	if !rt.Rate.HT && rt.Rate.Rate500 > 0 && float64(rt.Rate.Rate500)/2 < 6 {
		rt.Rate.CCK = true
	}
	return rt
}

// frame is the little we need from the 802.11 MAC header.
type frame struct {
	Type, Subtype int
	Retry         bool
	Addr1, Addr2  []byte // receiver, transmitter
	BSSID         []byte
	Unicast       bool
}

func parseFrame(w []byte) (frame, bool) {
	var f frame
	if len(w) < 10 {
		return f, false
	}
	f.Type = int(w[0]>>2) & 3
	f.Subtype = int(w[0]>>4) & 15
	f.Retry = w[1]&0x08 != 0
	toDS := w[1]&0x01 != 0
	fromDS := w[1]&0x02 != 0
	f.Addr1 = w[4:10]
	if len(w) >= 16 {
		f.Addr2 = w[10:16]
	}
	if len(w) >= 22 {
		switch {
		case !toDS && !fromDS:
			f.BSSID = w[16:22] // addr3
		case fromDS:
			f.BSSID = w[10:16] // addr2
		case toDS:
			f.BSSID = w[4:10] // addr1
		}
	}
	// Group addresses are not acknowledged, so they cost no ACK time.
	f.Unicast = len(f.Addr1) == 6 && f.Addr1[0]&0x01 == 0
	return f, true
}
