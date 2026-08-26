//go:build integration

package vessel

import (
	"context"
	"testing"
	"time"
)

// TestSummonWaitReapIntegration exercises the real Docker daemon.
// Run with: go test -tags integration ./internal/vessel
func TestSummonWaitReapIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	m, err := New(ctx)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}

	v, err := m.Summon(ctx, Spec{
		BeadID: "legion-test",
		Name:   "integration",
		Image:  "hello-world:latest",
	})
	if err != nil {
		t.Fatalf("summon: %v", err)
	}
	t.Cleanup(func() { _ = m.Reap(context.Background(), v.ID) })

	code, err := m.Wait(ctx, v.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	vs, err := m.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, got := range vs {
		if got.ID == v.ID && got.BeadID == "legion-test" {
			found = true
		}
	}
	if !found {
		t.Error("summoned vessel not found in List")
	}

	if err := m.Reap(ctx, v.ID); err != nil {
		t.Fatalf("reap: %v", err)
	}
}
