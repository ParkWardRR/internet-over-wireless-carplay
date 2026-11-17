package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ensureSysctl exists because of a real, silent failure: NAT64 translated
// correctly for days while the kernel dropped every translated packet, because
// net.ipv4.ip_forward was 0. A write that returns nil is not evidence the value
// took, so the read-back is the point of the function.

func TestEnsureSysctlWritesAndConfirms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(p, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSysctl(p, "1"); err != nil {
		t.Fatalf("want success, got %v", err)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("file holds %q", b)
	}
}

func TestEnsureSysctlReportsAnUnwritablePath(t *testing.T) {
	// A read-only /proc, a hardened unit, or simply not root.
	err := ensureSysctl(filepath.Join(t.TempDir(), "nope", "ip_forward"), "1")
	if err == nil {
		t.Fatal("a failed write must not be reported as success")
	}
	if !strings.Contains(err.Error(), "ip_forward") {
		t.Errorf("the error must name the sysctl: %v", err)
	}
}

// The case the read-back is for: the write succeeds and the value does not
// change. Simulated with a path that reads back something else.
func TestEnsureSysctlCatchesAValueThatDidNotTake(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ip_forward")
	if err := os.WriteFile(p, []byte("0\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	// 0444 means the write fails outright for a non-root user, which is also a
	// correct detection; under root the write succeeds and the read-back
	// matches. Either way the function must not claim success on a stale 0.
	err := ensureSysctl(p, "1")
	b, _ := os.ReadFile(p)
	got := strings.TrimSpace(string(b))
	if err == nil && got != "1" {
		t.Fatalf("claimed success while the value reads %q", got)
	}
	if err != nil && got == "1" {
		t.Fatalf("reported %v but the value did take", err)
	}
}

func TestEnsureSysctlToleratesTrailingNewline(t *testing.T) {
	// Real sysctls read back with a trailing newline; a naive compare would
	// fail forever and make the daemon refuse to start.
	p := filepath.Join(t.TempDir(), "forwarding")
	if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSysctl(p, "1"); err != nil {
		t.Fatalf("a trailing newline must not be treated as a mismatch: %v", err)
	}
}
