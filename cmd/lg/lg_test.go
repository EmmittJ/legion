package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/vessel"
)

type fakeBeads struct {
	created     []bead.CreateOpts
	createTitle string
	listed      map[string][]bead.Bead
}

func (f *fakeBeads) Create(_ context.Context, title string, opts bead.CreateOpts) (*bead.Bead, error) {
	f.createTitle = title
	f.created = append(f.created, opts)
	return &bead.Bead{ID: "legion-new", Title: title, Status: "open", Labels: opts.Labels, Priority: opts.Priority}, nil
}

func (f *fakeBeads) List(_ context.Context, status string) ([]bead.Bead, error) {
	return f.listed[status], nil
}

type fakeVessels struct {
	vessels []vessel.Vessel
	logs    string
	logged  string
}

func (f *fakeVessels) List(context.Context) ([]vessel.Vessel, error) { return f.vessels, nil }
func (f *fakeVessels) Logs(_ context.Context, id string, _ bool) (io.ReadCloser, error) {
	f.logged = id
	return io.NopCloser(strings.NewReader(f.logs)), nil
}

func newTestApp(b *fakeBeads, v *fakeVessels) (*app, *bytes.Buffer) {
	out := &bytes.Buffer{}
	a := &app{
		beads: b,
		vessels: func(context.Context) (vesselAPI, error) {
			if v == nil {
				return nil, errors.New("no docker")
			}
			return v, nil
		},
		out: out,
	}
	return a, out
}

func TestInvokeRoutingLabels(t *testing.T) {
	b := &fakeBeads{}
	a, out := newTestApp(b, nil)
	err := a.cmdInvoke(context.Background(), []string{"--vessel", "claude", "--persona", "reviewer", "-p", "1", "-d", "details", "Fix the bug"})
	if err != nil {
		t.Fatal(err)
	}
	if b.createTitle != "Fix the bug" {
		t.Errorf("title = %q", b.createTitle)
	}
	got := b.created[0]
	if got.Priority != 1 || got.Description != "details" || got.IssueType != "task" {
		t.Errorf("opts = %+v", got)
	}
	want := []string{"vessel:claude", "persona:reviewer"}
	if len(got.Labels) != 2 || got.Labels[0] != want[0] || got.Labels[1] != want[1] {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
	if !strings.Contains(out.String(), "legion-new") {
		t.Errorf("output = %q", out.String())
	}
}

func TestInvokeNoRoutingLabels(t *testing.T) {
	opts := invokeOpts("", "task", 2, "", "")
	if opts.Labels != nil {
		t.Errorf("labels = %v, want none", opts.Labels)
	}
}

func TestInvokeRequiresTitle(t *testing.T) {
	a, _ := newTestApp(&fakeBeads{}, nil)
	if err := a.cmdInvoke(context.Background(), nil); err == nil {
		t.Error("want usage error without title")
	}
}

func TestStatusTable(t *testing.T) {
	b := &fakeBeads{listed: map[string][]bead.Bead{
		"open":        {{ID: "legion-a", Title: "waiting", Status: "open", Priority: 2, Labels: []string{"vessel:claude"}}},
		"in_progress": {{ID: "legion-b", Title: "working", Status: "in_progress", Priority: 1}},
	}}
	v := &fakeVessels{vessels: []vessel.Vessel{{ID: "c1", BeadID: "legion-b", Name: "copilot", State: "running"}}}
	a, out := newTestApp(b, v)
	if err := a.cmdStatus(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"legion-a", "claude (pending)", "legion-b", "copilot (running)"} {
		if !strings.Contains(s, want) {
			t.Errorf("status output missing %q:\n%s", want, s)
		}
	}
}

func TestStatusDockerDownDegrades(t *testing.T) {
	b := &fakeBeads{listed: map[string][]bead.Bead{"open": {{ID: "legion-a", Title: "t", Status: "open"}}}}
	a, out := newTestApp(b, nil)
	if err := a.cmdStatus(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "vessel state unavailable") {
		t.Errorf("expected degraded warning, got:\n%s", out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	b := &fakeBeads{listed: map[string][]bead.Bead{"open": {{ID: "legion-a", Title: "t", Status: "open"}}}}
	a, out := newTestApp(b, &fakeVessels{})
	if err := a.cmdStatus(context.Background(), []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"legion-a"`) || !strings.HasPrefix(out.String(), "{") {
		t.Errorf("json output = %q", out.String())
	}
}

func TestLogFindsVesselByBead(t *testing.T) {
	v := &fakeVessels{
		vessels: []vessel.Vessel{{ID: "c9", BeadID: "legion-x", Name: "copilot", State: "exited"}},
		// stdcopy frame: stream 1, length 5, "hello"
		logs: string([]byte{1, 0, 0, 0, 0, 0, 0, 5}) + "hello",
	}
	a, out := newTestApp(&fakeBeads{}, v)
	if err := a.cmdLog(context.Background(), []string{"legion-x"}); err != nil {
		t.Fatal(err)
	}
	if v.logged != "c9" || out.String() != "hello" {
		t.Errorf("logged=%q out=%q", v.logged, out.String())
	}
}

func TestLogNoVessel(t *testing.T) {
	a, _ := newTestApp(&fakeBeads{}, &fakeVessels{})
	err := a.cmdLog(context.Background(), []string{"legion-x"})
	if err == nil || !strings.Contains(err.Error(), "no vessel found") {
		t.Errorf("err = %v", err)
	}
}

func TestInitWritesValidConfig(t *testing.T) {
	dir := t.TempDir()
	var cmds []string
	a, out := newTestApp(&fakeBeads{}, nil)
	a.run = func(_, name string, args ...string) (string, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		if name == "git" {
			return "https://github.com/acme/widgets.git\n", nil
		}
		return "", nil
	}
	if err := a.cmdInit(dir); err != nil {
		t.Fatal(err)
	}
	if len(cmds) == 0 || cmds[0] != "bd init" {
		t.Errorf("cmds = %v", cmds)
	}
	// The template must parse and validate with the real loader.
	cfg, err := config.Load(filepath.Join(dir, ".legion", "config.toml"))
	if err != nil {
		t.Fatalf("generated config invalid: %v", err)
	}
	if cfg.RepoURL != "https://github.com/acme/widgets.git" {
		t.Errorf("repo_url = %q", cfg.RepoURL)
	}
	if _, _, err := cfg.Image(""); err != nil {
		t.Errorf("default vessel unresolvable: %v", err)
	}
	if !strings.Contains(out.String(), "wrote .legion/config.toml") {
		t.Errorf("out = %q", out.String())
	}
}

func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".legion"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".legion", "config.toml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, out := newTestApp(&fakeBeads{}, nil)
	a.run = func(_, name string, _ ...string) (string, error) {
		t.Errorf("unexpected exec of %s", name)
		return "", nil
	}
	if err := a.cmdInit(dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already present") {
		t.Errorf("out = %q", out.String())
	}
}
