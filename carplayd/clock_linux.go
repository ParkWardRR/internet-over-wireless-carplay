package main

import (
	"syscall"
	"unsafe"
)

// boottime is CLOCK_BOOTTIME in seconds: the same base as /proc/uptime, so our
// events line up with the emulator's and the kernel's timestamps.
func boottime() float64 {
	var ts syscall.Timespec
	const clockBoottime = 7
	syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockBoottime, uintptr(unsafe.Pointer(&ts)), 0)
	return float64(ts.Sec) + float64(ts.Nsec)/1e9
}
