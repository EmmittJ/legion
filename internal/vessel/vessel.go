// Package vessel is Legion's execution primitive: summoning, watching,
// and reaping vessel containers via the Docker first-party Go SDK.
// A vessel is a container image with an ACP-speaking harness baked in;
// the animus inside possesses it. See ADR-0001.
package vessel

import (
	"context"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/docker/go-sdk/client"
	"github.com/docker/go-sdk/container"
	moby "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/telemetry"
)

// Container label keys identifying Legion-managed vessels.
const (
	LabelManaged = "legion.managed"
	LabelBeadID  = "legion.bead.id"
	LabelVessel  = "legion.vessel.name"
)

// Spec describes a vessel to summon for a bead.
type Spec struct {
	BeadID string            // the bead this vessel works
	Name   string            // vessel name from the registry (e.g. "copilot")
	Image  string            // resolved container image
	Env    map[string]string // environment for the animus (repo URL, tokens, …)
}

// Vessel is a summoned (or previously summoned) vessel container.
type Vessel struct {
	ID        string
	BeadID    string
	Name      string
	Image     string
	State     string // running, exited, …
	CreatedAt time.Time
}

// Running reports whether the vessel container is still running.
func (v *Vessel) Running() bool { return v.State == "running" }

// dockerAPI is the slice of the Docker SDK the manager uses. client.SDKClient
// satisfies it; tests fake it.
type dockerAPI interface {
	ContainerList(ctx context.Context, options mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error)
	ContainerWait(ctx context.Context, containerID string, options mobyclient.ContainerWaitOptions) mobyclient.ContainerWaitResult
	ContainerLogs(ctx context.Context, containerID string, options mobyclient.ContainerLogsOptions) (mobyclient.ContainerLogsResult, error)
	ContainerRemove(ctx context.Context, containerID string, options mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error)
}

// summonFn starts a container for a spec and returns its ID. Injectable for
// tests; production uses the SDK's container.Run.
type summonFn func(ctx context.Context, spec Spec, env map[string]string) (string, error)

// Manager summons and reaps vessels.
type Manager struct {
	api    dockerAPI
	summon summonFn
}

// Option configures a Manager.
type Option func(*Manager)

// withAPI / withSummon override Docker access; test-only.
func withAPI(api dockerAPI) Option { return func(m *Manager) { m.api = api } }
func withSummon(s summonFn) Option { return func(m *Manager) { m.summon = s } }

// New connects to the Docker daemon and returns a Manager.
func New(ctx context.Context, opts ...Option) (*Manager, error) {
	m := &Manager{}
	for _, o := range opts {
		o(m)
	}
	if m.api == nil {
		cli, err := client.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("docker client: %w", err)
		}
		m.api = cli
		if m.summon == nil {
			m.summon = func(ctx context.Context, spec Spec, env map[string]string) (string, error) {
				ctr, err := container.Run(ctx,
					container.WithClient(cli),
					container.WithImage(spec.Image),
					container.WithEnv(env),
					container.WithLabels(map[string]string{
						LabelManaged: "true",
						LabelBeadID:  spec.BeadID,
						LabelVessel:  spec.Name,
					}),
				)
				if err != nil {
					return "", err
				}
				return ctr.ID(), nil
			}
		}
	}
	return m, nil
}

func tracer() trace.Tracer { return otel.Tracer("legion/internal/vessel") }

func end(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// Summon starts a vessel for the spec. Trace context is injected into the
// vessel's environment (TRACEPARENT) so the animus joins the bead's trace.
func (m *Manager) Summon(ctx context.Context, spec Spec) (*Vessel, error) {
	ctx, span := tracer().Start(ctx, "vessel.summon", trace.WithAttributes(
		attribute.String(telemetry.AttrBeadID, spec.BeadID),
		attribute.String(telemetry.AttrVesselName, spec.Name),
	))
	env := map[string]string{"LEGION_BEAD_ID": spec.BeadID}
	maps.Copy(env, spec.Env)
	maps.Copy(env, telemetry.InjectEnv(ctx))

	id, err := m.summon(ctx, spec, env)
	end(span, err)
	if err != nil {
		return nil, fmt.Errorf("summon vessel for %s: %w", spec.BeadID, err)
	}
	return &Vessel{ID: id, BeadID: spec.BeadID, Name: spec.Name, Image: spec.Image, State: "running", CreatedAt: time.Now()}, nil
}

// List returns all Legion-managed vessels, running or exited.
func (m *Manager) List(ctx context.Context) ([]Vessel, error) {
	ctx, span := tracer().Start(ctx, "vessel.list")
	res, err := m.api.ContainerList(ctx, mobyclient.ContainerListOptions{
		All:     true,
		Filters: make(mobyclient.Filters).Add("label", LabelManaged+"=true"),
	})
	end(span, err)
	if err != nil {
		return nil, err
	}
	vessels := make([]Vessel, 0, len(res.Items))
	for _, c := range res.Items {
		vessels = append(vessels, Vessel{
			ID:        c.ID,
			BeadID:    c.Labels[LabelBeadID],
			Name:      c.Labels[LabelVessel],
			Image:     c.Image,
			State:     string(c.State),
			CreatedAt: time.Unix(c.Created, 0),
		})
	}
	return vessels, nil
}

// Wait blocks until the vessel exits and returns its exit code.
func (m *Manager) Wait(ctx context.Context, id string) (int64, error) {
	ctx, span := tracer().Start(ctx, "vessel.wait")
	res := m.api.ContainerWait(ctx, id, mobyclient.ContainerWaitOptions{
		Condition: moby.WaitConditionNotRunning,
	})
	select {
	case r := <-res.Result:
		var err error
		if r.Error != nil {
			err = fmt.Errorf("vessel %s wait: %s", id, r.Error.Message)
		}
		end(span, err)
		return r.StatusCode, err
	case err := <-res.Error:
		end(span, err)
		return -1, err
	case <-ctx.Done():
		end(span, ctx.Err())
		return -1, ctx.Err()
	}
}

// Logs streams the vessel's combined stdout/stderr.
func (m *Manager) Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error) {
	return m.api.ContainerLogs(ctx, id, mobyclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
	})
}

// Reap force-removes an exited (or hung) vessel.
func (m *Manager) Reap(ctx context.Context, id string) error {
	ctx, span := tracer().Start(ctx, "vessel.reap")
	_, err := m.api.ContainerRemove(ctx, id, mobyclient.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	end(span, err)
	return err
}
