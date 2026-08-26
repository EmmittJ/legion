package vessel

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	moby "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
)

type fakeAPI struct {
	listRes   mobyclient.ContainerListResult
	listErr   error
	listOpts  mobyclient.ContainerListOptions
	waitCode  int64
	waitErr   error
	logs      string
	removed   []string
	removeErr error
}

func (f *fakeAPI) ContainerList(_ context.Context, o mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	f.listOpts = o
	return f.listRes, f.listErr
}

func (f *fakeAPI) ContainerWait(_ context.Context, _ string, _ mobyclient.ContainerWaitOptions) mobyclient.ContainerWaitResult {
	resultC := make(chan moby.WaitResponse, 1)
	errC := make(chan error, 1)
	if f.waitErr != nil {
		errC <- f.waitErr
	} else {
		resultC <- moby.WaitResponse{StatusCode: f.waitCode}
	}
	return mobyclient.ContainerWaitResult{Result: resultC, Error: errC}
}

type fakeLogs struct{ io.Reader }

func (fakeLogs) Close() error { return nil }

func (f *fakeAPI) ContainerLogs(_ context.Context, _ string, _ mobyclient.ContainerLogsOptions) (mobyclient.ContainerLogsResult, error) {
	return fakeLogs{strings.NewReader(f.logs)}, nil
}

func (f *fakeAPI) ContainerRemove(_ context.Context, id string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
	f.removed = append(f.removed, id)
	return mobyclient.ContainerRemoveResult{}, f.removeErr
}

func newTestManager(t *testing.T, api *fakeAPI, summon summonFn) *Manager {
	t.Helper()
	m, err := New(context.Background(), withAPI(api), withSummon(summon))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSummonInjectsBeadEnvAndLabels(t *testing.T) {
	var gotSpec Spec
	var gotEnv map[string]string
	m := newTestManager(t, &fakeAPI{}, func(_ context.Context, spec Spec, env map[string]string) (string, error) {
		gotSpec, gotEnv = spec, env
		return "ctr-1", nil
	})
	v, err := m.Summon(context.Background(), Spec{
		BeadID: "legion-1", Name: "copilot", Image: "legion/vessel-copilot:latest",
		Env: map[string]string{"REPO_URL": "https://example.com/r.git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "ctr-1" || v.BeadID != "legion-1" || v.Name != "copilot" {
		t.Errorf("unexpected vessel: %+v", v)
	}
	if gotSpec.Image != "legion/vessel-copilot:latest" {
		t.Errorf("spec not passed through: %+v", gotSpec)
	}
	if gotEnv["LEGION_BEAD_ID"] != "legion-1" || gotEnv["REPO_URL"] != "https://example.com/r.git" {
		t.Errorf("env missing entries: %v", gotEnv)
	}
}

func TestSummonError(t *testing.T) {
	boom := errors.New("no such image")
	m := newTestManager(t, &fakeAPI{}, func(context.Context, Spec, map[string]string) (string, error) {
		return "", boom
	})
	if _, err := m.Summon(context.Background(), Spec{BeadID: "legion-2"}); !errors.Is(err, boom) {
		t.Fatalf("want wrapped summon error, got %v", err)
	}
}

func TestListFiltersManagedAndMapsLabels(t *testing.T) {
	api := &fakeAPI{listRes: mobyclient.ContainerListResult{Items: []moby.Summary{{
		ID:    "ctr-9",
		Image: "legion/vessel-claude:latest",
		State: "exited",
		Labels: map[string]string{
			LabelManaged: "true", LabelBeadID: "legion-7", LabelVessel: "claude",
		},
	}}}}
	m := newTestManager(t, api, nil)
	vs, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].BeadID != "legion-7" || vs[0].Name != "claude" || vs[0].State != "exited" {
		t.Errorf("unexpected vessels: %+v", vs)
	}
	if !api.listOpts.All {
		t.Error("List must include exited vessels (All)")
	}
	if !api.listOpts.Filters["label"][LabelManaged+"=true"] {
		t.Errorf("List must filter on %s=true: %v", LabelManaged, api.listOpts.Filters)
	}
}

func TestWaitReturnsExitCode(t *testing.T) {
	m := newTestManager(t, &fakeAPI{waitCode: 3}, nil)
	code, err := m.Wait(context.Background(), "ctr-1")
	if err != nil || code != 3 {
		t.Fatalf("Wait = (%d, %v), want (3, nil)", code, err)
	}
}

func TestWaitError(t *testing.T) {
	m := newTestManager(t, &fakeAPI{waitErr: errors.New("daemon gone")}, nil)
	if _, err := m.Wait(context.Background(), "ctr-1"); err == nil {
		t.Fatal("want wait error")
	}
}

func TestLogsAndReap(t *testing.T) {
	api := &fakeAPI{logs: "possessed"}
	m := newTestManager(t, api, nil)
	rc, err := m.Logs(context.Background(), "ctr-1", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "possessed" {
		t.Errorf("logs = %q", b)
	}
	if err := m.Reap(context.Background(), "ctr-1"); err != nil {
		t.Fatal(err)
	}
	if len(api.removed) != 1 || api.removed[0] != "ctr-1" {
		t.Errorf("reap removed %v", api.removed)
	}
}
