// emuagent runs inside the emulated OpenWrt box and does the timing-critical
// work the busybox shell can't: polling a condition every 20 ms and stamping
// events with a millisecond CLOCK_BOOTTIME clock (same base as /proc/uptime,
// so its EV lines interleave with emu.sh's).
//
//	emuagent ev <event> [detail...]
//	emuagent wait <condition> [car] [timeout_s]
//
// Conditions: box-assoc, box-disassoc, phone-assoc A|B, phone-disassoc A|B,
// phone-route A|B, phone-noroute A|B. Each prints "EV <t> <event> ..." when
// it becomes true, or "EV <t> timeout <condition>" and exits 1.
package main

import (
	"fmt"
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
