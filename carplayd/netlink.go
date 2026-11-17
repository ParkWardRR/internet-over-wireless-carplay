package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// isNotFound reports whether the kernel is saying "that address was already
// gone", which is the ordinary case when a link is torn down twice.
func isNotFound(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.ENOENT)
}

// netOps is the rtnetlink surface carplayd needs. It exists to replace calls
// out to iproute2: every one of them was a fork, an exec, a text format to
// parse and a dependency to install on the box. The interface is deliberately
// narrow -- it is exactly the six things the daemon does to a link, no more --
// so a fake in a test is three lines and the real one is a thin shim.
//
// Text parsing was not just inconvenient. `ip -6 route show dev X` prints
// on-link and gatewayed routes in the same shape, and telling them apart meant
// looking for the word "via"; rtnetlink answers with a struct whose Gw field is
// nil or isn't. The policy that used to sit downstream of a parser now sits on
// typed data.
type netOps interface {
	// LinkUp brings a netdev up. Idempotent.
	LinkUp(dev string) error
	// AddrReplace adds or replaces an address. With noDAD the address is
	// usable immediately, which is why the first RA isn't held back.
	AddrReplace(dev string, p netip.Prefix, noDAD bool) error
	// AddrDel removes an address. Missing is not an error.
	AddrDel(dev string, p netip.Prefix) error
	// Neighbours lists the IPv6 neighbour cache for a device.
	Neighbours(dev string) ([]neighbour, error)
	// OnLinkRoutes6 lists the device's on-link IPv6 prefixes -- routes with a
	// gateway are the internet, not a local network, and are excluded here
	// rather than by the caller.
	OnLinkRoutes6(dev string) ([]netip.Prefix, error)
}

// net returns the daemon's kernel interface, defaulting to the real one. The
// zero value of daemon is used all over the tests, so this must work without
// anyone having set the field; a test that cares injects a fake instead.
func (d *daemon) net() netOps {
	if d.nl == nil {
		return rtnetlink{}
	}
	return d.nl
}

// rtnetlink is the real implementation, talking to the kernel directly.
type rtnetlink struct{}

func (rtnetlink) LinkUp(dev string) error {
	l, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("link %s: %w", dev, err)
	}
	if err := netlink.LinkSetUp(l); err != nil {
		return fmt.Errorf("link %s up: %w", dev, err)
	}
	return nil
}

func (rtnetlink) AddrReplace(dev string, p netip.Prefix, noDAD bool) error {
	l, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("link %s: %w", dev, err)
	}
	a := &netlink.Addr{IPNet: prefixToIPNet(p)}
	if noDAD {
		a.Flags |= unix.IFA_F_NODAD
	}
	if err := netlink.AddrReplace(l, a); err != nil {
		return fmt.Errorf("addr replace %s dev %s: %w", p, dev, err)
	}
	return nil
}

func (rtnetlink) AddrDel(dev string, p netip.Prefix) error {
	l, err := netlink.LinkByName(dev)
	if err != nil {
		return fmt.Errorf("link %s: %w", dev, err)
	}
	// Deleting an address that isn't there is the normal case on a second
	// stop, and must not be reported as a failure.
	if err := netlink.AddrDel(l, &netlink.Addr{IPNet: prefixToIPNet(p)}); err != nil && !isNotFound(err) {
		return fmt.Errorf("addr del %s dev %s: %w", p, dev, err)
	}
	return nil
}

func (rtnetlink) Neighbours(dev string) ([]neighbour, error) {
	l, err := netlink.LinkByName(dev)
	if err != nil {
		return nil, fmt.Errorf("link %s: %w", dev, err)
	}
	ns, err := netlink.NeighList(l.Attrs().Index, netlink.FAMILY_V6)
	if err != nil {
		return nil, fmt.Errorf("neigh %s: %w", dev, err)
	}
	out := make([]neighbour, 0, len(ns))
	for _, n := range ns {
		a, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		out = append(out, neighbour{
			addr:  a.Unmap(),
			mac:   n.HardwareAddr.String(),
			state: neighState(n.State),
		})
	}
	return out, nil
}

func (rtnetlink) OnLinkRoutes6(dev string) ([]netip.Prefix, error) {
	l, err := netlink.LinkByName(dev)
	if err != nil {
		return nil, fmt.Errorf("link %s: %w", dev, err)
	}
	rs, err := netlink.RouteList(l, netlink.FAMILY_V6)
	if err != nil {
		return nil, fmt.Errorf("route %s: %w", dev, err)
	}
	var out []netip.Prefix
	for _, r := range rs {
		if p, ok := onLinkRoute(r.Dst, r.Gw); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// onLinkRoute decides whether one route is a network this link is directly
// attached to, and returns its prefix.
//
// This is the judgement that used to be made by looking for the word "via" in
// `ip -6 route show` output, and it is the one piece of the old text parsing
// with real consequences: a prefix wrongly treated as on-link gets added to
// the car's block list, and one wrongly discarded leaves a hole in the
// isolation. Separated out so it can be tested without a kernel.
func onLinkRoute(dst *net.IPNet, gw net.IP) (netip.Prefix, bool) {
	// A gateway means "reachable through a router": the internet, not one of
	// this link's own networks.
	if gw != nil {
		return netip.Prefix{}, false
	}
	// A nil destination is the default route.
	if dst == nil {
		return netip.Prefix{}, false
	}
	return ipNetToPrefix(dst)
}

// formatNeigh renders the neighbour cache for the debug log in the one-line
// shape the log has always had, so older logs and newer ones read the same.
func formatNeigh(ns []neighbour) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		if n.mac == "" {
			parts = append(parts, fmt.Sprintf("%s %s", n.addr, n.state))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s lladdr %s %s", n.addr, n.mac, n.state))
	}
	return strings.Join(parts, " | ")
}

func prefixToIPNet(p netip.Prefix) *net.IPNet {
	return &net.IPNet{
		IP:   p.Addr().AsSlice(),
		Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen()),
	}
}

func ipNetToPrefix(n *net.IPNet) (netip.Prefix, bool) {
	a, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := n.Mask.Size()
	return netip.PrefixFrom(a.Unmap(), ones), true
}

// neighState renders the kernel's NUD bitmask as the state word iproute2 would
// have printed, because that is what the withdrawal policy is written against
// and what every event in the log already says.
func neighState(state int) string {
	for _, s := range []struct {
		bit  int
		name string
	}{
		{0x01, "INCOMPLETE"},
		{0x02, "REACHABLE"},
		{0x04, "STALE"},
		{0x08, "DELAY"},
		{0x10, "PROBE"},
		{0x20, "FAILED"},
		{0x40, "NOARP"},
		{0x80, "PERMANENT"},
	} {
		if state&s.bit != 0 {
			return s.name
		}
	}
	return "NONE"
}
