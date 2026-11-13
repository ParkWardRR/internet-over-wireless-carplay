package main

import (
	"fmt"
	"strings"
)

// Apple Device IE: vendor-specific element (ID 221), OUI 00:A0:40, vendor
// type 0. The body is read here as a run of sub-TLVs, each id(1) len(1)
// value(len), with sub-ID 0 holding a flag bit array.
//
// PROVENANCE. Unlike the Interworking element above, none of this is from a
// specification we can cite. The sub-ID meanings and the flag-bit names come
// from third-party copies of Apple's AccessorySDK IEEE80211Utils.h and from
// open-source head-unit projects (luka-dev/mib2q-carplay-rgi,
// gyqworld/liunx-CarPlay); Apple's public material (WWDC17 session 717) says
// only that the Apple Device IE carries internet state, not how. Treat every
// name this file prints as a label on a bit whose meaning is unconfirmed, and
// the bit numbering (bit 0 = most significant bit of the first flag octet) as
// an assumption inherited from those same sources.
//
// Consequently this file decodes and validates. It deliberately does not build
// an Apple Device IE for transmission: a wrong guess emitted into a beacon is
// an experiment that cannot be interpreted. Trials C and D in
// docs/ap-signaling.md stay blocked until a capture from a real
// internet-capable wireless CarPlay head unit settles the encoding.

var appleOUI = [3]byte{0x00, 0xa0, 0x40}

const appleVendorType = 0x00

// appleFlagNames is indexed by bit number, counting bit 0 as the most
// significant bit of flag octet 0. Unverified; see the provenance note above.
var appleFlagNames = []string{
	"AirPlay", "Unconfigured", "MFiConfigV1", "WakeOnWireless",
	"InterferenceRobustness", "PPPoEServer", "WPSCapable", "WPSActive",
	"AirPrint", "iAPoverWiFi", "CarPlay", "InternetAccess", "ACPConfigV1",
	"UserContact", "2.4GHz", "5GHz", "PINConfig", "HomeKit",
}

// internetAccessBit is the bit this project would be testing if the encoding
// were ever confirmed. Named so the claim has exactly one home in the code.
const internetAccessBit = 11

var appleSubNames = map[byte]string{
	0x00: "flags", 0x01: "name", 0x02: "manufacturer", 0x03: "model",
	0x06: "bt-mac", 0x07: "device-id",
}

type appleSub struct {
	ID    byte
	Value []byte
}

type appleIE struct {
	Subs     []appleSub
	HasFlags bool
	Flags    []byte
}

// FlagSet reports whether bit n is set, using the MSB-first numbering above.
func (a appleIE) FlagSet(n int) bool {
	if !a.HasFlags || n < 0 || n/8 >= len(a.Flags) {
		return false
	}
	return a.Flags[n/8]&(0x80>>(n%8)) != 0
}

// parseAppleIE decodes the body of a vendor element that has already had its
// OUI and vendor type stripped.
func parseAppleIE(body []byte) (appleIE, error) {
	var a appleIE
	i := 0
	for i < len(body) {
		if len(body)-i == 1 {
			return a, fmt.Errorf("malformed: Apple Device IE has a trailing octet at sub-offset %d (sub-ID %d with no length octet)", i, body[i])
		}
		id, n := body[i], int(body[i+1])
		if i+2+n > len(body) {
			return a, fmt.Errorf("malformed: Apple Device IE sub-ID %d at sub-offset %d declares length %d but only %d octets remain", id, i, n, len(body)-i-2)
		}
		v := body[i+2 : i+2+n]
		if id == 0x00 {
			if n == 0 {
				return a, fmt.Errorf("malformed: Apple Device IE flags sub-element at sub-offset %d is empty", i)
			}
			a.HasFlags, a.Flags = true, v
		}
		a.Subs = append(a.Subs, appleSub{ID: id, Value: v})
		i += 2 + n
	}
	if len(a.Subs) == 0 {
		return a, fmt.Errorf("malformed: Apple Device IE carries no sub-elements")
	}
	return a, nil
}

func (a appleIE) String() string {
	var parts []string
	for _, s := range a.Subs {
		switch s.ID {
		case 0x00:
			var set []string
			for n := 0; n < len(s.Value)*8; n++ {
				if !a.FlagSet(n) {
					continue
				}
				if n < len(appleFlagNames) {
					set = append(set, appleFlagNames[n])
				} else {
					set = append(set, fmt.Sprintf("bit%d", n))
				}
			}
			names := "none"
			if len(set) > 0 {
				names = strings.Join(set, ",")
			}
			parts = append(parts, fmt.Sprintf("flags=%s[%s] (names unverified)", hexs(s.Value), names))
		case 0x01, 0x02, 0x03:
			parts = append(parts, fmt.Sprintf("%s=%q", appleSubNames[s.ID], printable(s.Value)))
		case 0x06, 0x07:
			// Identity-bearing: length only, never the value.
			parts = append(parts, fmt.Sprintf("%s=<%d octets, withheld>", appleSubNames[s.ID], len(s.Value)))
		default:
			parts = append(parts, fmt.Sprintf("sub%d=%s", s.ID, hexs(s.Value)))
		}
	}
	return strings.Join(parts, " ")
}

func printable(b []byte) string {
	out := make([]rune, 0, len(b))
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			out = append(out, '.')
			continue
		}
		out = append(out, rune(c))
	}
	return string(out)
}
