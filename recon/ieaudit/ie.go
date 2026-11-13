package main

import "fmt"

// Generic 802.11 information-element parsing (802.11-2020 9.4.2).
//
// Every element is ID(1) Length(1) Body(Length). Element ID 255 means the first
// body octet is an Element ID Extension. Nothing here trusts a declared length:
// a length that runs past the end of the buffer, a trailing ID with no length
// octet, and an extension element with an empty body are all errors, because the
// point of this tool is to catch an IE we built or captured wrong, not to guess
// what it meant.

type element struct {
	ID     byte
	ExtID  byte // meaningful only when ID == 255
	HasExt bool
	Body   []byte // for ID 255 this excludes the extension-ID octet
	Offset int    // offset of the ID octet within the blob
}

// parseElements splits a buffer into elements. It returns everything it managed
// to parse along with the first malformation, so a caller can report both.
func parseElements(b []byte) ([]element, error) {
	var out []element
	i := 0
	for i < len(b) {
		if len(b)-i == 1 {
			return out, fmt.Errorf("malformed: trailing octet at offset %d (element ID %d with no length octet)", i, b[i])
		}
		id, n := b[i], int(b[i+1])
		if i+2+n > len(b) {
			return out, fmt.Errorf("malformed: element ID %d at offset %d declares length %d but only %d octets remain", id, i, n, len(b)-i-2)
		}
		e := element{ID: id, Body: b[i+2 : i+2+n], Offset: i}
		if id == 255 {
			if n == 0 {
				return out, fmt.Errorf("malformed: extension element at offset %d has an empty body (no Element ID Extension octet)", i)
			}
			e.HasExt, e.ExtID, e.Body = true, e.Body[0], e.Body[1:]
		}
		out = append(out, e)
		i += 2 + n
	}
	return out, nil
}

// vendorOUI returns the OUI and vendor type of a vendor-specific element
// (ID 221). A vendor element shorter than 4 octets cannot carry both, which is
// malformed rather than merely uninteresting.
func vendorOUI(e element) (oui [3]byte, vtype byte, body []byte, err error) {
	if e.ID != 221 {
		return oui, 0, nil, fmt.Errorf("element ID %d is not vendor-specific", e.ID)
	}
	if len(e.Body) < 4 {
		return oui, 0, nil, fmt.Errorf("malformed: vendor element at offset %d has %d octets, need at least 4 (OUI + type)", e.Offset, len(e.Body))
	}
	copy(oui[:], e.Body[:3])
	return oui, e.Body[3], e.Body[4:], nil
}

func hexs(b []byte) string {
	const d = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, d[c>>4], d[c&0xf])
	}
	return string(out)
}
