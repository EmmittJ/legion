package tests

import "testing"

// TestHello is a minimal smoke test that verifies the test harness is wired
// up correctly. If this fails, something is wrong with the Go toolchain or
// module configuration — not the application code.
func TestHello(t *testing.T) {
	got := hello()
	if got != "hello" {
		t.Errorf("hello() = %q, want %q", got, "hello")
	}
}

func hello() string { return "hello" }
