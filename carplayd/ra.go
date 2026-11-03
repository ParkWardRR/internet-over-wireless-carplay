package main

import (
	"encoding/binary"
	"net"
	"net/netip"
	"time"
)

// RA option and message constants (RFC 4861, RFC 8106).
const (
	icmpRS        = 133
	icmpRA        = 134
	optSourceLL   = 1
	optPrefixInfo = 3
	optRDNSS      = 25
)

// raParams is what goes into one Router Advertisement.
type raParams struct {
	RouterLife    time.Duration // 0 = "stop using me as a router" (goodbye)
	Prefix        netip.Prefix
	ValidLife     time.Duration
	PreferredLife time.Duration
	DNS           []netip.Addr
	DNSLife       time.Duration
	SourceMAC     net.HardwareAddr
}

func secs(d time.Duration, max uint32) uint32 {
	s := d / time.Second
	if s < 0 {
		return 0
	}
	if uint64(s) > uint64(max) {
		return max
	}
	return uint32(s)
}

// buildRA returns the ICMPv6 Router Advertisement body. The kernel fills in
// the checksum on raw ICMPv6 sockets.
func buildRA(p raParams) []byte {
	b := make([]byte, 16, 128)
	b[0] = icmpRA
	b[4] = 64 // cur hop limit
	b[5] = 0  // M=0 O=0: SLAAC only
	binary.BigEndian.PutUint16(b[6:], uint16(secs(p.RouterLife, 9000)))
	// reachable time and retrans timer stay 0 (unspecified)

	if len(p.SourceMAC) == 6 {
		o := make([]byte, 8)
		o[0], o[1] = optSourceLL, 1
		copy(o[2:], p.SourceMAC)
		b = append(b, o...)
	}
	if p.Prefix.IsValid() {
		o := make([]byte, 32)
		o[0], o[1] = optPrefixInfo, 4
		o[2] = byte(p.Prefix.Bits())
		o[3] = 0xc0 // on-link + autonomous
		binary.BigEndian.PutUint32(o[4:], secs(p.ValidLife, 0xffffffff))
		binary.BigEndian.PutUint32(o[8:], secs(p.PreferredLife, 0xffffffff))
		a := p.Prefix.Masked().Addr().As16()
		copy(o[16:], a[:])
		b = append(b, o...)
	}
	if len(p.DNS) > 0 {
		o := make([]byte, 8+16*len(p.DNS))
		o[0], o[1] = optRDNSS, byte(1+2*len(p.DNS))
		binary.BigEndian.PutUint32(o[4:], secs(p.DNSLife, 0xffffffff))
		for i, d := range p.DNS {
			a := d.As16()
			copy(o[8+16*i:], a[:])
		}
		b = append(b, o...)
	}
	return b
}

// withdrawn is p for a router that is going away or has lost its upstream
// (RFC 4861 6.2.5, RFC 8106 5.1): router lifetime 0 drops the default route,
// RDNSS lifetime 0 drops the DNS servers, preferred lifetime 0 deprecates the
// address so new connections use cellular. The valid lifetime stays: hosts
// ignore cuts below two hours anyway (RFC 4862 5.5.3e), and open connections
// may finish.
func (p raParams) withdrawn() raParams {
	p.RouterLife, p.DNSLife, p.PreferredLife = 0, 0, 0
	return p
}
