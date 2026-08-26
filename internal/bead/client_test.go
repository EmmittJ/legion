package bead

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRoutingLabels(t *testing.T) {
	b := Bead{Labels: []string{"vessel:claude", "persona:reviewer", "other"}}
	if got := b.Vessel(); got != "claude" {
		t.Errorf("Vessel() = %q, want claude", got)
	}
	if got := b.Persona(); got != "reviewer" {
		t.Errorf("Persona() = %q, want reviewer", got)
	}
	empty := Bead{}
	if empty.Vessel() != "" || empty.Persona() != "" {
		t.Error("unlabeled bead should route to defaults")
	}
	if VesselLabel("copilot") != "vessel:copilot" || PersonaLabel("dev") != "persona:dev" {
		t.Error("label builders wrong")
	}
}

func TestPrompt(t *testing.T) {
	b := Bead{ID: "legion-1", Title: "Do the thing", Description: "details", Acceptance: "it works"}
	p := b.Prompt()
	for _, want := range []string{"Do the thing", "legion-1", "details", "Acceptance criteria:", "it works"} {
		if !strings.Contains(p, want) {
			t.Errorf("Prompt() missing %q:\n%s", want, p)
		}
	}
}

type call struct {
	args []string
	out  string
	err  error
}

func fakeClient(t *testing.T, calls ...call) (*Client, *int) {
	t.Helper()
	i := new(int)
	return New(withRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if *i >= len(calls) {
			t.Fatalf("unexpected bd call: %v", args)
		}
		c := calls[*i]
		*i++
		if strings.Join(args, " ") != strings.Join(c.args, " ") {
			t.Errorf("call %d: got args %v, want %v", *i, args, c.args)
		}
		return []byte(c.out), c.err
	})), i
}

func TestReadyParsesArray(t *testing.T) {
	c, n := fakeClient(t, call{
		args: []string{"ready", "--json"},
		out:  `[{"id":"legion-1","title":"a","labels":["vessel:copilot"]}]`,
	})
	bs, err := c.Ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].ID != "legion-1" || bs[0].Vessel() != "copilot" {
		t.Errorf("unexpected beads: %+v", bs)
	}
	if *n != 1 {
		t.Errorf("expected 1 call, got %d", *n)
	}
}

func TestGetSingleObjectFallback(t *testing.T) {
	c, _ := fakeClient(t, call{
		args: []string{"show", "legion-2", "--json"},
		out:  `{"id":"legion-2","title":"solo"}`,
	})
	b, err := c.Get(context.Background(), "legion-2")
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "legion-2" {
		t.Errorf("got %+v", b)
	}
}

func TestGetNotFound(t *testing.T) {
	c, _ := fakeClient(t, call{args: []string{"show", "legion-x", "--json"}, out: `[]`})
	if _, err := c.Get(context.Background(), "legion-x"); err == nil {
		t.Fatal("want error for missing bead")
	}
}

func TestCreateBuildsArgs(t *testing.T) {
	c, _ := fakeClient(t, call{
		args: []string{"create", "New work", "-p", "1", "-d", "desc", "-t", "task",
			"-l", "vessel:claude,persona:reviewer", "--deps", "discovered-from:legion-9", "--json"},
		out: `[{"id":"legion-3","title":"New work"}]`,
	})
	b, err := c.Create(context.Background(), "New work", CreateOpts{
		Description:    "desc",
		IssueType:      "task",
		Priority:       1,
		Labels:         []string{VesselLabel("claude"), PersonaLabel("reviewer")},
		DiscoveredFrom: "legion-9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "legion-3" {
		t.Errorf("got %+v", b)
	}
}

func TestFailTracesThenReopens(t *testing.T) {
	c, n := fakeClient(t,
		call{args: []string{"comment", "legion-4", "FAILED: harness crashed", "--json"}, out: `{}`},
		call{args: []string{"update", "legion-4", "--status", "open", "--json"}, out: `[]`},
	)
	if err := c.Fail(context.Background(), "legion-4", "harness crashed"); err != nil {
		t.Fatal(err)
	}
	if *n != 2 {
		t.Errorf("expected 2 calls, got %d", *n)
	}
}

func TestErrorsPropagate(t *testing.T) {
	boom := errors.New("bd exploded")
	c, _ := fakeClient(t, call{args: []string{"ready", "--json"}, err: boom})
	if _, err := c.Ready(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("want wrapped error, got %v", err)
	}
}

func TestDoltSync(t *testing.T) {
	c, n := fakeClient(t,
		call{args: []string{"dolt", "pull"}, out: ""},
		call{args: []string{"dolt", "push"}, out: ""},
	)
	if err := c.DoltPull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.DoltPush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *n != 2 {
		t.Errorf("expected 2 calls, got %d", *n)
	}
}
