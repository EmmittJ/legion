package archon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/vessel"
)

type fakeBeads struct {
	ready    []bead.Bead
	readyErr error
	claimed  []string
	claimErr error
	closed   map[string]string
	failed   map[string]string
}

func newFakeBeads() *fakeBeads {
	return &fakeBeads{closed: map[string]string{}, failed: map[string]string{}}
}

func (f *fakeBeads) Ready(context.Context) ([]bead.Bead, error) { return f.ready, f.readyErr }
func (f *fakeBeads) Claim(_ context.Context, id string) error {
	f.claimed = append(f.claimed, id)
	return f.claimErr
}
func (f *fakeBeads) Close(_ context.Context, id, reason string) error {
	f.closed[id] = reason
	return nil
}
func (f *fakeBeads) Fail(_ context.Context, id, reason string) error {
	f.failed[id] = reason
	return nil
}

type fakeVessels struct {
	list     []vessel.Vessel
	summoned []vessel.Spec
	summErr  error
	waits    map[string]int64
	reaped   []string
}

func newFakeVessels() *fakeVessels { return &fakeVessels{waits: map[string]int64{}} }

func (f *fakeVessels) Summon(_ context.Context, s vessel.Spec) (*vessel.Vessel, error) {
	f.summoned = append(f.summoned, s)
	if f.summErr != nil {
		return nil, f.summErr
	}
	return &vessel.Vessel{ID: "ctr-" + s.BeadID, BeadID: s.BeadID, Name: s.Name}, nil
}
func (f *fakeVessels) List(context.Context) ([]vessel.Vessel, error) { return f.list, nil }
func (f *fakeVessels) Wait(_ context.Context, id string) (int64, error) {
	return f.waits[id], nil
}
func (f *fakeVessels) Reap(_ context.Context, id string) error {
	f.reaped = append(f.reaped, id)
	return nil
}

func testConfig() *config.Config {
	return &config.Config{
		RepoURL:       "https://example.com/r.git",
		DefaultVessel: "copilot",
		Vessels: map[string]string{
			"copilot": "img/copilot:latest",
			"claude":  "img/claude:latest",
		},
		Archon: config.Archon{
			PollInterval: config.Duration{Duration: time.Second},
			MaxVessels:   2,
			BeadTimeout:  config.Duration{Duration: 30 * time.Minute},
		},
	}
}

func TestSummonsReadyBeadsUpToCap(t *testing.T) {
	beads := newFakeBeads()
	beads.ready = []bead.Bead{
		{ID: "legion-1"},
		{ID: "legion-2", Labels: []string{"vessel:claude"}},
		{ID: "legion-3"}, // over the cap of 2
	}
	pool := newFakeVessels()
	r := &Reconciler{Beads: beads, Vessels: pool, Config: testConfig(), Env: map[string]string{"GH_TOKEN": "t"}}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pool.summoned) != 2 {
		t.Fatalf("summoned %d vessels, want 2 (cap)", len(pool.summoned))
	}
	if pool.summoned[0].Image != "img/copilot:latest" || pool.summoned[0].Name != "copilot" {
		t.Errorf("default routing wrong: %+v", pool.summoned[0])
	}
	if pool.summoned[1].Image != "img/claude:latest" || pool.summoned[1].Name != "claude" {
		t.Errorf("label routing wrong: %+v", pool.summoned[1])
	}
	if pool.summoned[0].Env["LEGION_REPO_URL"] != "https://example.com/r.git" ||
		pool.summoned[0].Env["GH_TOKEN"] != "t" {
		t.Errorf("env wrong: %v", pool.summoned[0].Env)
	}
	if len(beads.claimed) != 2 {
		t.Errorf("claimed %v, want both summoned beads", beads.claimed)
	}
}

func TestSkipsBeadsWithExistingVessels(t *testing.T) {
	beads := newFakeBeads()
	beads.ready = []bead.Bead{{ID: "legion-1"}}
	pool := newFakeVessels()
	pool.list = []vessel.Vessel{{ID: "ctr-1", BeadID: "legion-1", State: "running", CreatedAt: time.Now()}}
	r := &Reconciler{Beads: beads, Vessels: pool, Config: testConfig()}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pool.summoned) != 0 {
		t.Errorf("summoned %v, want none", pool.summoned)
	}
}

func TestReapClosesOnZeroFailsOnNonzero(t *testing.T) {
	beads := newFakeBeads()
	pool := newFakeVessels()
	pool.list = []vessel.Vessel{
		{ID: "ctr-ok", BeadID: "legion-1", Name: "copilot", State: "exited"},
		{ID: "ctr-bad", BeadID: "legion-2", Name: "copilot", State: "exited"},
	}
	pool.waits["ctr-ok"] = 0
	pool.waits["ctr-bad"] = 1
	r := &Reconciler{Beads: beads, Vessels: pool, Config: testConfig()}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := beads.closed["legion-1"]; !ok {
		t.Error("legion-1 should be closed on exit 0")
	}
	if _, ok := beads.failed["legion-2"]; !ok {
		t.Error("legion-2 should be failed on exit 1")
	}
	if len(pool.reaped) != 2 {
		t.Errorf("reaped %v, want both containers", pool.reaped)
	}
}

func TestTimeoutFailsBeadAndReaps(t *testing.T) {
	beads := newFakeBeads()
	pool := newFakeVessels()
	pool.list = []vessel.Vessel{{
		ID: "ctr-old", BeadID: "legion-1", Name: "copilot", State: "running",
		CreatedAt: time.Now().Add(-time.Hour),
	}}
	r := &Reconciler{Beads: beads, Vessels: pool, Config: testConfig()}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := beads.failed["legion-1"]; !ok {
		t.Error("timed-out bead should be failed")
	}
	if len(pool.reaped) != 1 || pool.reaped[0] != "ctr-old" {
		t.Errorf("reaped %v", pool.reaped)
	}
}

func TestUnroutableBeadFailsFast(t *testing.T) {
	beads := newFakeBeads()
	beads.ready = []bead.Bead{{ID: "legion-1", Labels: []string{"vessel:ghost"}}}
	pool := newFakeVessels()
	r := &Reconciler{Beads: beads, Vessels: pool, Config: testConfig()}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pool.summoned) != 0 {
		t.Error("unroutable bead must not summon")
	}
	if _, ok := beads.failed["legion-1"]; !ok {
		t.Error("unroutable bead should be failed")
	}
}

func TestSummonErrorFailsBead(t *testing.T) {
	beads := newFakeBeads()
	beads.ready = []bead.Bead{{ID: "legion-1"}}
	pool := newFakeVessels()
	pool.summErr = errors.New("no such image")
	r := &Reconciler{Beads: beads, Vessels: pool, Config: testConfig()}

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := beads.failed["legion-1"]; !ok {
		t.Error("summon failure should fail the bead")
	}
}
