package main

import (
	"fmt"
	"strings"
)

// 802.11u Interworking element, ID 107 (802.11-2020 9.4.2.91).
//
//	Access Network Options   1 octet   always present
//	Venue Info               2 octets  optional
//	HESSID                   6 octets  optional
//
// so the only legal body lengths are 1, 3, 7 and 9. Access Network Options:
// bits 0-3 Access Network Type, bit 4 Internet, bit 5 ASRA, bit 6 ESR,
// bit 7 UESA.
//
// This is the standards-based half of the experiment: hostapd writes it from
// interworking=1 / access_network_type= / internet=, so a mismatch between the
// configured value and the captured one is a tooling bug, not a protocol
// unknown. That is exactly what makes it worth checking first.

const interworkingElemID = 107

type interworking struct {
	Raw               []byte
	AccessNetworkType byte
	Internet          bool
	ASRA              bool // Additional Step Required for Access
	ESR               bool // Emergency Services Reachable
	UESA              bool // Unauthenticated Emergency Service Accessible
	HasVenue          bool
	VenueGroup        byte
	VenueType         byte
	HasHESSID         bool
	HESSID            []byte
}

var accessNetworkTypes = map[byte]string{
	0: "private", 1: "private with guest access", 2: "chargeable public",
	3: "free public", 4: "personal device", 5: "emergency services only",
	14: "test or experimental", 15: "wildcard",
}

var venueGroups = map[byte]string{
	0: "unspecified", 1: "assembly", 2: "business", 3: "educational",
	4: "factory and industrial", 5: "institutional", 6: "mercantile",
	7: "residential", 8: "storage", 9: "utility and misc", 10: "vehicular",
	11: "outdoor",
}

func parseInterworking(body []byte) (interworking, error) {
	w := interworking{Raw: body}
	switch len(body) {
	case 1, 3, 7, 9:
	default:
		return w, fmt.Errorf("malformed: Interworking element has %d octets; 802.11-2020 9.4.2.91 allows 1, 3, 7 or 9", len(body))
	}
	ano := body[0]
	w.AccessNetworkType = ano & 0x0f
	w.Internet = ano&0x10 != 0
	w.ASRA = ano&0x20 != 0
	w.ESR = ano&0x40 != 0
	w.UESA = ano&0x80 != 0
	rest := body[1:]
	if len(rest) == 2 || len(rest) == 8 {
		w.HasVenue, w.VenueGroup, w.VenueType = true, rest[0], rest[1]
		rest = rest[2:]
	}
	if len(rest) == 6 {
		w.HasHESSID, w.HESSID = true, rest
	}
	return w, nil
}

func (w interworking) String() string {
	t, ok := accessNetworkTypes[w.AccessNetworkType]
	if !ok {
		t = fmt.Sprintf("reserved(%d)", w.AccessNetworkType)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "interworking internet=%d type=%d(%s)", b2i(w.Internet), w.AccessNetworkType, t)
	for _, f := range []struct {
		name string
		set  bool
	}{{"ASRA", w.ASRA}, {"ESR", w.ESR}, {"UESA", w.UESA}} {
		if f.set {
			b.WriteString(" " + f.name)
		}
	}
	if w.HasVenue {
		g, ok := venueGroups[w.VenueGroup]
		if !ok {
			g = fmt.Sprintf("reserved(%d)", w.VenueGroup)
		}
		fmt.Fprintf(&b, " venue=%s/%d", g, w.VenueType)
	}
	if w.HasHESSID {
		fmt.Fprintf(&b, " hessid=%s", maskMAC(w.HESSID))
	}
	fmt.Fprintf(&b, " raw=%s", hexs(w.Raw))
	return b.String()
}

func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}
