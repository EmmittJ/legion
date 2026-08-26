package animus

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/EmmittJ/legion/internal/acp"
	"github.com/EmmittJ/legion/internal/bead"
)

type fakeBeads struct {
	bead    *bead.Bead
	getErr  error
	traces  []string
	pushed  bool
	created []bead.CreateOpts
}

func (f *fakeBeads) Get(_ context.Context, id string) (*bead.Bead, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.bead, nil
}
func (f *fakeBeads) Trace(_ context.Context, _ string, text string) error {
	f.traces = append(f.traces, text)
	return nil
}
func (f *fakeBeads) DoltPush(context.Context) error { f.pushed = true; return nil }
func (f *fakeBeads) Create(_ context.Context, title string, opts bead.CreateOpts) (*bead.Bead, error) {
	f.created = append(f.created, opts)
	return &bead.Bead{ID: "legion-new", Title: title}, nil
}
func (f *fakeBeads) Children(context.Context, string) ([]bead.Bead, error) {
	return []bead.Bead{{ID: "legion-kid", Title: "child"}}, nil
}

type fakeSession struct {
	prompts []string
	stop    acpsdk.StopReason
	err     error
	closed  bool
}

func (f *fakeSession) Prompt(_ context.Context, text string) (acpsdk.StopReason, error) {
	f.prompts = append(f.prompts, text)
	return f.stop, f.err
}
func (f *fakeSession) Close() error { f.closed = true; return nil }

type gitCall struct {
	dir  string
	args string
}

func testDeps(beads *fakeBeads, sess *fakeSession, env map[string]string) (Deps, *[]gitCall, *acp.Config) {
	gits := &[]gitCall{}
	var gotCfg acp.Config
	d := Deps{
		Beads: beads,
		RunGit: func(_ context.Context, dir string, args ...string) (string, error) {
			*gits = append(*gits, gitCall{dir: dir, args: strings.Join(args, " ")})
			return "", nil
		},
		StartSession: func(_ context.Context, cfg acp.Config) (Session, error) {
			gotCfg = cfg
			return sess, nil
		},
		Lookup:  func(k string) string { return env[k] },
		WorkDir: "/work",
		SelfExe: "/usr/local/bin/animus",
	}
	return d, gits, &gotCfg
}

func baseEnv() map[string]string {
	return map[string]string{
		EnvBeadID:     "legion-1",
		EnvRepoURL:    "https://example.com/r.git",
		EnvHarnessCmd: "copilot --stdio",
	}
}

func TestPossessHappyPath(t *testing.T) {
	beads := &fakeBeads{bead: &bead.Bead{ID: "legion-1", Title: "Do the thing", Description: "details"}}
	sess := &fakeSession{stop: acpsdk.StopReasonEndTurn}
	d, gits, cfg := testDeps(beads, sess, baseEnv())

	if err := Possess(context.Background(), d); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join("/work", "repo")
	wantGit := []gitCall{
		{dir: "/work", args: "clone https://example.com/r.git " + repoDir},
		{dir: repoDir, args: "checkout -b legion/legion-1"},
		{dir: repoDir, args: "push origin legion/legion-1"},
	}
	if len(*gits) != len(wantGit) {
		t.Fatalf("git calls = %+v", *gits)
	}
	for i, w := range wantGit {
		if (*gits)[i] != w {
			t.Errorf("git[%d] = %+v, want %+v", i, (*gits)[i], w)
		}
	}

	if got := cfg.Command; strings.Join(got, " ") != "copilot --stdio" {
		t.Errorf("harness command = %v", got)
	}
	if cfg.Cwd != repoDir {
		t.Errorf("session cwd = %q", cfg.Cwd)
	}
	if len(cfg.McpServers) != 1 || cfg.McpServers[0].Stdio == nil ||
		cfg.McpServers[0].Stdio.Command != "/usr/local/bin/animus" {
		t.Errorf("mcp servers = %+v", cfg.McpServers)
	}

	if len(sess.prompts) != 1 || !strings.Contains(sess.prompts[0], "Do the thing") {
		t.Errorf("prompts = %v", sess.prompts)
	}
	if !sess.closed {
		t.Error("session not closed")
	}
	if !beads.pushed {
		t.Error("bd dolt push not called")
	}
}

func TestPossessPersonaPassthrough(t *testing.T) {
	env := baseEnv()
	env[EnvPersonaFlag] = "--agent"
	beads := &fakeBeads{bead: &bead.Bead{ID: "legion-1", Title: "t", Labels: []string{"persona:reviewer"}}}
	sess := &fakeSession{stop: acpsdk.StopReasonEndTurn}
	d, _, cfg := testDeps(beads, sess, env)

	if err := Possess(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	want := "copilot --stdio --agent reviewer"
	if got := strings.Join(cfg.Command, " "); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestPossessPersonaIgnoredWithoutFlag(t *testing.T) {
	beads := &fakeBeads{bead: &bead.Bead{ID: "legion-1", Title: "t", Labels: []string{"persona:reviewer"}}}
	sess := &fakeSession{stop: acpsdk.StopReasonEndTurn}
	d, _, cfg := testDeps(beads, sess, baseEnv())

	if err := Possess(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Command, " "); got != "copilot --stdio" {
		t.Errorf("command = %q; persona must not leak without a declared flag", got)
	}
}

func TestPossessRequiresEnv(t *testing.T) {
	d, _, _ := testDeps(&fakeBeads{}, &fakeSession{}, map[string]string{})
	if err := Possess(context.Background(), d); err == nil {
		t.Fatal("want error for missing env")
	}
}

func TestPossessAbortedTurnFails(t *testing.T) {
	beads := &fakeBeads{bead: &bead.Bead{ID: "legion-1", Title: "t"}}
	sess := &fakeSession{stop: acpsdk.StopReasonRefusal}
	d, gits, _ := testDeps(beads, sess, baseEnv())

	if err := Possess(context.Background(), d); err == nil {
		t.Fatal("want error on refusal")
	}
	for _, g := range *gits {
		if strings.HasPrefix(g.args, "push") {
			t.Error("must not push on aborted turn")
		}
	}
}

func TestPossessCloneFailure(t *testing.T) {
	beads := &fakeBeads{bead: &bead.Bead{ID: "legion-1", Title: "t"}}
	d, _, _ := testDeps(beads, &fakeSession{}, baseEnv())
	d.RunGit = func(context.Context, string, ...string) (string, error) {
		return "", errors.New("auth failed")
	}
	err := Possess(context.Background(), d)
	if err == nil || !strings.Contains(err.Error(), "clone") {
		t.Fatalf("want clone error, got %v", err)
	}
}

func TestHarnessCommandValidation(t *testing.T) {
	if _, err := harnessCommand(func(string) string { return "" }, ""); err == nil {
		t.Error("empty HARNESS_CMD must error")
	}
}

func TestMCPServerTools(t *testing.T) {
	beads := &fakeBeads{bead: &bead.Bead{ID: "legion-1", Title: "Do the thing", Status: "in_progress"}}
	server := MCPServer(beads, "legion-1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned tool error: %+v", name, res.Content)
		}
		return res
	}

	res := call("bead_get", nil)
	if txt, ok := res.Content[0].(*mcp.TextContent); !ok || !strings.Contains(txt.Text, "Do the thing") {
		t.Errorf("bead_get content = %+v", res.Content)
	}

	call("bead_trace", map[string]any{"text": "progress"})
	if len(beads.traces) != 1 || beads.traces[0] != "progress" {
		t.Errorf("traces = %v", beads.traces)
	}

	call("bead_discover", map[string]any{"title": "found bug", "priority": 1})
	if len(beads.created) != 1 || beads.created[0].DiscoveredFrom != "legion-1" || beads.created[0].Priority != 1 {
		t.Errorf("created = %+v", beads.created)
	}

	res = call("bead_children", nil)
	if txt, ok := res.Content[0].(*mcp.TextContent); !ok || !strings.Contains(txt.Text, "legion-kid") {
		t.Errorf("bead_children content = %+v", res.Content)
	}

	// Missing required arg surfaces as a tool error, not a crash.
	tr, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "bead_trace", Arguments: map[string]any{}})
	if err == nil && !tr.IsError {
		t.Error("bead_trace with no text should error")
	}
}
