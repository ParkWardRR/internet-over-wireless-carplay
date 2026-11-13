package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

// A minimal pcap reader, enough for `tcpdump -I -w` on a monitor-mode radio.
// pcapng is not supported: tcpdump writes classic pcap by default and a reader
// that silently mis-parses a capture would undermine the one job this tool has.

const (
	linkIEEE80211         = 105
	linkIEEE80211Radiotap = 127
)

type pcapReader struct {
	r        io.Reader
	bo       binary.ByteOrder
	nanos    bool
	linkType uint32
	snaplen  uint32
	hdr      [16]byte
	buf      []byte
}

func openPcap(path string) (*pcapReader, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	p, err := newPcapReader(f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, f.Close, nil
}

func newPcapReader(r io.Reader) (*pcapReader, error) {
	var h [24]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, fmt.Errorf("short pcap file header: %w", err)
	}
	p := &pcapReader{r: r}
	switch be, le := binary.BigEndian.Uint32(h[:4]), binary.LittleEndian.Uint32(h[:4]); {
	case le == 0xa1b2c3d4:
		p.bo = binary.LittleEndian
	case le == 0xa1b23c4d:
		p.bo, p.nanos = binary.LittleEndian, true
	case be == 0xa1b2c3d4:
		p.bo = binary.BigEndian
	case be == 0xa1b23c4d:
		p.bo, p.nanos = binary.BigEndian, true
	default:
		return nil, fmt.Errorf("not a pcap file (magic %08x); pcapng is not supported, capture with `tcpdump -w`", le)
	}
	p.snaplen = p.bo.Uint32(h[16:20])
	p.linkType = p.bo.Uint32(h[20:24])
	switch p.linkType {
	case linkIEEE80211, linkIEEE80211Radiotap:
	default:
		return nil, fmt.Errorf("link type %d is not 802.11; capture on a monitor-mode interface (`iw dev X set type monitor`)", p.linkType)
	}
	return p, nil
}

type packet struct {
	Time    time.Time
	Data    []byte // 802.11 frame, radiotap already stripped
	Signal  int
	Trunc   bool // the capture was cut short of the on-air length
	OrigLen int
}

// next returns the next 802.11 frame. io.EOF ends the file.
func (p *pcapReader) next() (packet, error) {
	if _, err := io.ReadFull(p.r, p.hdr[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return packet{}, fmt.Errorf("truncated packet header at end of file")
		}
		return packet{}, err
	}
	sec, frac := p.bo.Uint32(p.hdr[0:4]), p.bo.Uint32(p.hdr[4:8])
	inclLen, origLen := p.bo.Uint32(p.hdr[8:12]), p.bo.Uint32(p.hdr[12:16])
	if inclLen > 1<<20 {
		return packet{}, fmt.Errorf("packet claims %d octets, refusing", inclLen)
	}
	if cap(p.buf) < int(inclLen) {
		p.buf = make([]byte, inclLen)
	}
	b := p.buf[:inclLen]
	if _, err := io.ReadFull(p.r, b); err != nil {
		return packet{}, fmt.Errorf("truncated packet body: %w", err)
	}
	nsec := int64(frac) * 1000
	if p.nanos {
		nsec = int64(frac)
	}
	pk := packet{Time: time.Unix(int64(sec), nsec), OrigLen: int(origLen), Trunc: origLen > inclLen}
	if p.linkType == linkIEEE80211Radiotap {
		hlen, sig, ok := parseRadiotap(b)
		if !ok || hlen > len(b) {
			return packet{}, fmt.Errorf("unparsable radiotap header")
		}
		pk.Data, pk.Signal = b[hlen:], sig
	} else {
		pk.Data = b
	}
	return pk, nil
}

// radiotap field alignments and sizes, in presence-bit order (field 5 is the
// antenna signal, the only one used here).
var rtFields = [...][2]int{
	{8, 8}, {1, 1}, {1, 1}, {2, 4}, {2, 2}, {1, 1}, {1, 1}, {2, 2},
	{2, 2}, {2, 2}, {1, 1}, {1, 1}, {1, 1}, {1, 1}, {2, 2}, {2, 2},
}

func parseRadiotap(b []byte) (hlen, signal int, ok bool) {
	if len(b) < 8 || b[0] != 0 {
		return 0, 0, false
	}
	hlen = int(binary.LittleEndian.Uint16(b[2:4]))
	if hlen < 8 || hlen > len(b) {
		return 0, 0, false
	}
	off := 4
	present := binary.LittleEndian.Uint32(b[off:])
	first := present
	for present&(1<<31) != 0 {
		off += 4
		if off+4 > hlen {
			return hlen, 0, true
		}
		present = binary.LittleEndian.Uint32(b[off:])
	}
	pos := off + 4
	for i := range rtFields {
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
		if i == 5 {
			signal = int(int8(b[pos]))
		}
		pos += size
	}
	return hlen, signal, true
}

// mgmtBody splits a management frame into its subtype, transmitter (BSSID for
// beacons and probe responses), and the element blob that follows the fixed
// fields. ok is false for anything that is not a beacon or probe response.
func mgmtBody(w []byte) (subtype int, bssid []byte, elems []byte, ok bool) {
	// Management header: FC(2) Duration(2) A1(6) A2(6) A3(6) SeqCtl(2) = 24,
	// then Timestamp(8) BeaconInterval(2) Capability(2) = 12 fixed octets.
	const hdr, fixed = 24, 12
	if len(w) < hdr+fixed {
		return 0, nil, nil, false
	}
	if w[0]&0x0c != 0 { // type bits: 0 = management
		return 0, nil, nil, false
	}
	subtype = int(w[0]>>4) & 0x0f
	if subtype != 8 && subtype != 5 { // beacon, probe response
		return 0, nil, nil, false
	}
	return subtype, w[16:22], w[hdr+fixed:], true
}

func subtypeName(s int) string {
	if s == 8 {
		return "beacon"
	}
	return "probe-resp"
}
