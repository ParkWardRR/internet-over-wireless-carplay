// emuagent runs inside the emulated OpenWrt box and does the timing-critical
// work the busybox shell can't: polling a condition every 20 ms and stamping
// events with a millisecond CLOCK_BOOTTIME clock (same base as /proc/uptime,
// so its EV lines interleave with emu.sh's).
//
//	emuagent ev <event> [detail...]
//	emuagent wait <condition> [car] [timeout_s]
//	emuagent rssi <phy> <dBm>        set how strongly every radio hears <phy>
//	emuagent tcp <name> <[addr]:port>  can this namespace connect there?
//	emuagent rs <dev>                send one Router Solicitation
//
// Conditions: box-assoc, box-disassoc, phone-assoc A|B, phone-disassoc A|B,
// phone-route A|B, phone-noroute A|B. Each prints "EV <t> <event> ..." when
// it becomes true, or "EV <t> timeout <condition>" and exits 1.
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const eventsFile = "/tmp/emu/events"

// boottime returns CLOCK_BOOTTIME in seconds, like /proc/uptime but to the ns.
func boottime() float64 {
	var ts syscall.Timespec
	const clockBoottime = 7
	syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockBoottime, uintptr(unsafe.Pointer(&ts)), 0)
	return float64(ts.Sec) + float64(ts.Nsec)/1e9
}

func ev(fields ...string) {
	line := fmt.Sprintf("EV %.3f %s", boottime(), strings.Join(fields, " "))
	fmt.Println(line)
	if f, err := os.OpenFile(eventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

func run(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	return string(out)
}

func inNS(ns string, args ...string) string {
	return run("ip", append([]string{"netns", "exec", ns}, args...)...)
}

func operstate(dev string) string {
	b, err := os.ReadFile("/sys/class/net/" + dev + "/operstate")
	if err != nil {
		return "absent"
	}
	return strings.TrimSpace(string(b))
}

// linkField pulls "SSID" or "signal" out of `iw dev X link`.
func linkField(out, key string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, key+":") {
			f := strings.Fields(strings.TrimPrefix(l, key+":"))
			if len(f) > 0 {
				return f[0]
			}
		}
	}
	return "?"
}

func lower(car string) string { return strings.ToLower(car) }

// check reports whether cond holds and the event fields to print if so.
func check(cond, car string) (bool, []string) {
	phone := "phone" + car
	phDev := "ph-" + lower(car) + "0"
	switch cond {
	case "box-assoc":
		// The STA netdev goes "up" only once associated and the 4-way handshake is done.
		if operstate("phy0-sta0") == "up" {
			l := run("iw", "dev", "phy0-sta0", "link")
			return true, []string{"box_assoc", linkField(l, "SSID"), linkField(l, "signal")}
		}
	case "box-disassoc":
		if operstate("phy0-sta0") != "up" {
			return true, []string{"box_disassoc"}
		}
	case "phone-assoc":
		if strings.Contains(inNS(phone, "iw", "dev", phDev, "link"), "Connected") {
			return true, []string{"phone_assoc", car}
		}
	case "phone-disassoc":
		if !strings.Contains(inNS(phone, "iw", "dev", phDev, "link"), "Connected") {
			return true, []string{"phone_disassoc", car}
		}
	case "phone-route":
		if strings.Contains(inNS(phone, "ip", "-6", "route", "show", "default"), "via") {
			addr := "?"
			for _, f := range strings.Fields(inNS(phone, "ip", "-6", "-o", "addr", "show", "dev", phDev, "scope", "global")) {
				if strings.Contains(f, "/64") {
					addr = f
					break
				}
			}
			return true, []string{"phone_route", car, addr}
		}
	case "phone-noroute":
		if !strings.Contains(inNS(phone, "ip", "-6", "route", "show", "default"), "via") {
			return true, []string{"phone_noroute", car}
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown condition", cond)
		os.Exit(2)
	}
	return false, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: emuagent ev <event> [detail...] | wait <condition> [car] [timeout_s]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ev":
		ev(os.Args[2:]...)
	case "rssi":
		// mac80211_hwsim: every receiver hears a radio at that radio's rx_rssi
		// plus its tx power. The debugfs file parses unsigned, so write the
		// two's complement; the driver casts it back to int.
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		v, err := strconv.ParseInt(os.Args[3], 10, 64)
		if err != nil || v >= 0 || v < -100 {
			fmt.Fprintln(os.Stderr, "rssi: want -100..-1 dBm")
			os.Exit(2)
		}
		p := "/sys/kernel/debug/ieee80211/" + os.Args[2] + "/hwsim/rx_rssi"
		if err := os.WriteFile(p, []byte(strconv.FormatUint(uint64(v), 10)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "rs":
		// emuagent rs <dev>: send one Router Solicitation to ff02::2 from dev's
		// link-local (the kernel fills in the checksum), as a host does on link-up.
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		if err := sendRS(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "rs:", err)
			os.Exit(1)
		}
		ev("rs_sent", os.Args[2])
	case "stream":
		// emuagent stream <ip:port> <bytes/s> <seconds>: UDP at a steady rate,
		// standing in for the CarPlay video/audio stream phone → head unit.
		if len(os.Args) != 5 {
			os.Exit(2)
		}
		rate, _ := strconv.Atoi(os.Args[3])
		secs, _ := strconv.ParseFloat(os.Args[4], 64)
		c, err := net.Dial("udp", os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		pkt := make([]byte, 1200)
		per := time.Second * time.Duration(len(pkt)) / time.Duration(max(rate, 1))
		ev("stream_begin", os.Args[2], os.Args[3])
		for end := time.Now().Add(time.Duration(secs * float64(time.Second))); time.Now().Before(end); time.Sleep(per) {
			c.Write(pkt)
		}
		ev("stream_end", os.Args[2])
	case "tcp":
		// emuagent tcp <name> <[addr]:port>: can this namespace open a TCP
		// connection there? Prints "EV <t> reach <name> open|blocked <addr>".
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		state := "open"
		if c, err := net.DialTimeout("tcp", os.Args[3], 2*time.Second); err != nil {
			state = "blocked"
		} else {
			c.Close()
		}
		ev("reach", os.Args[2], state, os.Args[3])
	case "wait":
		if len(os.Args) < 3 {
			os.Exit(2)
		}
		cond, car, timeout := os.Args[2], "", 60.0
		rest := os.Args[3:]
		if len(rest) > 0 && (rest[0] == "A" || rest[0] == "B") {
			car, rest = rest[0], rest[1:]
		}
		if len(rest) > 0 {
			if t, err := strconv.ParseFloat(rest[0], 64); err == nil {
				timeout = t
			}
		}
		deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
		for {
			if ok, fields := check(cond, car); ok {
				ev(fields...)
				return
			}
			if time.Now().After(deadline) {
				ev("timeout", cond, car)
				os.Exit(1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}

func sendRS(dev string) error {
	c, err := net.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		return err
	}
	defer c.Close()
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		return err
	}
	rc, err := c.(*net.IPConn).SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	rc.Control(func(fd uintptr) {
		// ND messages must carry hop limit 255 (RFC 4861 6.1.1).
		if serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_HOPS, 255); serr == nil {
			serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_IF, ifi.Index)
		}
	})
	if serr != nil {
		return serr
	}
	rs := []byte{133, 0, 0, 0, 0, 0, 0, 0} // type, code, checksum, reserved
	_, err = c.WriteTo(rs, &net.IPAddr{IP: net.ParseIP("ff02::2"), Zone: dev})
	return err
}
