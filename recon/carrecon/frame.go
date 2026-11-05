package main

import (
	"encoding/binary"
	"fmt"
)

// frame is what carrecon needs from one captured 802.11 frame.
type frame struct {
	ftype, subtype int    // 0 mgmt, 1 ctrl, 2 data
	retry          bool   // retransmission
	protected      bool   // encrypted payload
	toDS, fromDS   bool   //
	a1, a2, a3     string // receiver, transmitter, third address
	length         int    // bytes on air (as captured, without radiotap)
	signal         int    // dBm, 0 = unknown
	rateKbps       int    // legacy rate, 0 = unknown (HT frames)
	eapol          bool   // unencrypted EAPOL (WPA handshake)
	reason         int    // deauth/disassoc reason code
}

func mac(b []byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// radiotap field alignment and size, in present-bit order (fields 0..15).
var rtFields = [][2]int{
	{8, 8}, // 0 TSFT
	{1, 1}, // 1 flags
	{1, 1}, // 2 rate (500 kb/s)
	{2, 4}, // 3 channel
	{2, 2}, // 4 FHSS
	{1, 1}, // 5 dBm antenna signal
	{1, 1}, // 6 dBm antenna noise
	{2, 2}, // 7 lock quality
	{2, 2}, // 8 TX attenuation
	{2, 2}, // 9 dB TX attenuation
	{1, 1}, // 10 dBm TX power
	{1, 1}, // 11 antenna
	{1, 1}, // 12 dB antenna signal
	{1, 1}, // 13 dB antenna noise
	{2, 2}, // 14 RX flags
	{2, 2}, // 15 TX flags (unused here)
}

// parseRadiotap returns the header length, signal (dBm) and legacy rate.
func parseRadiotap(b []byte) (hlen, signal, rateKbps int, ok bool) {
	if len(b) < 8 || b[0] != 0 {
		return 0, 0, 0, false
	}
	hlen = int(binary.LittleEndian.Uint16(b[2:]))
	if hlen > len(b) {
		return 0, 0, 0, false
	}
	// Present words: bit 31 set means another word follows.
	off := 4
	present := binary.LittleEndian.Uint32(b[off:])
	first := present
	for present&(1<<31) != 0 {
		off += 4
		if off+4 > hlen {
			return hlen, 0, 0, true
		}
		present = binary.LittleEndian.Uint32(b[off:])
	}
	pos := off + 4
	for i := 0; i < len(rtFields); i++ {
		if first&(1<<uint(i)) == 0 {
			continue
		}
		align, size := rtFields[i][0], rtFields[i][1]
		if r := pos % align; r != 0 {
			pos += align - r
		}
		if pos+size > hlen {
			break
		}
		switch i {
		case 2:
			rateKbps = int(b[pos]) * 500
		case 5:
			signal = int(int8(b[pos]))
		}
		pos += size
	}
	return hlen, signal, rateKbps, true
}

// parseFrame decodes radiotap + the 802.11 MAC header. origLen is the frame's
// length on the wire (the capture may be truncated to a snap length); 0 = len(b).
func parseFrame(b []byte, origLen int) (frame, bool) {
	hlen, sig, rate, ok := parseRadiotap(b)
	if !ok || len(b) < hlen+10 {
		return frame{}, false
	}
	w := b[hlen:]
	fc0, fc1 := w[0], w[1]
	f := frame{
		ftype: int(fc0>>2) & 3, subtype: int(fc0>>4) & 15,
		toDS: fc1&1 != 0, fromDS: fc1&2 != 0, retry: fc1&8 != 0, protected: fc1&0x40 != 0,
		length: max(origLen, len(b)) - hlen, signal: sig, rateKbps: rate,
	}
	if len(w) >= 10 {
		f.a1 = mac(w[4:10])
	}
	if len(w) >= 16 {
		f.a2 = mac(w[10:16])
	}
	if len(w) >= 22 {
		f.a3 = mac(w[16:22])
	}
	switch {
	case f.ftype == 0 && (f.subtype == 10 || f.subtype == 12) && len(w) >= 26:
		f.reason = int(binary.LittleEndian.Uint16(w[24:]))
	case f.ftype == 2 && !f.protected:
		// Data header 24 bytes (+2 QoS for subtypes 8-15), then LLC/SNAP.
		h := 24
		if f.subtype&8 != 0 {
			h += 2
		}
		if len(w) >= h+8 && w[h] == 0xaa && w[h+1] == 0xaa && w[h+6] == 0x88 && w[h+7] == 0x8e {
			f.eapol = true
		}
	}
	return f, true
}

// bssid returns the BSS a frame belongs to (by DS bits), or "".
func (f frame) bssid() string {
	switch {
	case f.ftype == 0:
		return f.a3
	case f.ftype == 2 && f.toDS && !f.fromDS:
		return f.a1
	case f.ftype == 2 && !f.toDS && f.fromDS:
		return f.a2
	}
	return ""
}

// station returns the non-AP end of a frame in a BSS, or "".
func (f frame) station() string {
	switch {
	case f.ftype == 2 && f.toDS && !f.fromDS:
		return f.a2
	case f.ftype == 2 && !f.toDS && f.fromDS:
		return f.a1
	case f.ftype == 0 && f.a2 == f.a3:
		return f.a1
	case f.ftype == 0:
		return f.a2
	}
	return ""
}
