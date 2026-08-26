// Package archon is Legion's reconciler: each tick maps beads that want
// work onto vessels doing work — summoning vessels for ready beads and
// reaping finished ones. Archon is the sole authority that closes or
// fails a bead, so a crashed vessel can never leave a zombie bead.
// State is reconstructed every tick from Beads + Docker; there is no
// local state file.
package archon

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/telemetry"
	"github.com/EmmittJ/legion/internal/vessel"
)

// beadStore is the slice of the bead layer Archon uses. *bead.Client
// satisfies it; tests fake it.
type beadStore interface {
	Ready(ctx context.Context) ([]bead.Bead, error)
	Claim(ctx context.Context, id string) error
	Close(ctx context.Context, id, reason string) error
	Fail(ctx context.Context, id, reason string) error
}

// vesselPool is the slice of the vessel layer Archon uses.
// *vessel.Manager satisfies it; tests fake it.
type vesselPool interface {
	Summon(ctx context.Context, spec vessel.Spec) (*vessel.Vessel, error)
	List(ctx context.Context) ([]vessel.Vessel, error)
	Wait(ctx context.Context, id string) (int64, error)
	Reap(ctx context.Context, id string) error
}

// Reconciler drives the summon/reap loop.
type Reconciler struct {
	Beads   beadStore
	Vessels vesselPool
	Config  *config.Config
	// Env is merged into every summoned vessel's environment
	// (repo URL, tokens, OTLP endpoint, …).
	Env map[string]string
}

func tracer() trace.Tracer { return otel.Tracer("legion/internal/archon") }

// Tick runs one reconcile pass: reap exited vessels, fail timed-out ones,
// then summon vessels for ready beads up to the concurrency cap.
func (r *Reconciler) Tick(ctx context.Context) error {
	ctx, span := tracer().Start(ctx, "archon.reconcile")
	defer span.End()

	vessels, err := r.Vessels.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("list vessels: %w", err)
	}

	running := 0
	byBead := make(map[string]bool, len(vessels))
	for _, v := range vessels {
		byBead[v.BeadID] = true
		if !v.Running() {
			r.reap(ctx, v)
			continue
		}
		if r.Config.Archon.BeadTimeout.Duration > 0 &&
			time.Since(v.CreatedAt) > r.Config.Archon.BeadTimeout.Duration {
			r.timeout(ctx, v)
			continue
		}
		running++
	}

	ready, err := r.Beads.Ready(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("list ready beads: %w", err)
	}

	summoned := 0
	for _, b := range ready {
		if running+summoned >= r.Config.Archon.MaxVessels {
			break
		}
		if byBead[b.ID] {
			continue // a vessel for this bead already exists
		}
		if r.summon(ctx, b) {
			summoned++
		}
	}

	span.SetAttributes(
		attribute.Int("legion.vessels.running", running),
		attribute.Int("legion.vessels.summoned", summoned),
		attribute.Int("legion.beads.ready", len(ready)),
	)
	return nil
}

// summon starts one bead's vessel. Returns true when a vessel now runs.
func (r *Reconciler) summon(ctx context.Context, b bead.Bead) bool {
	ctx, span := tracer().Start(ctx, "bead.work", trace.WithAttributes(
		attribute.String(telemetry.AttrBeadID, b.ID),
	))
	defer span.End()

	name, image, err := r.Config.Image(b.Vessel())
	if err != nil {
		// Unroutable bead: fail it so it doesn't wedge the queue.
		slog.ErrorContext(ctx, "bead unroutable", "bead", b.ID, "error", err)
		r.fail(ctx, b.ID, err.Error())
		span.SetStatus(codes.Error, err.Error())
		return false
	}
	span.SetAttributes(
		attribute.String(telemetry.AttrVesselName, name),
		attribute.String(telemetry.AttrPersona, b.Persona()),
	)

	if err := r.Beads.Claim(ctx, b.ID); err != nil {
		slog.ErrorContext(ctx, "claim failed", "bead", b.ID, "error", err)
		span.SetStatus(codes.Error, err.Error())
		return false
	}

	env := map[string]string{"LEGION_REPO_URL": r.Config.RepoURL}
	maps.Copy(env, r.Env)
	v, err := r.Vessels.Summon(ctx, vessel.Spec{
		BeadID: b.ID,
		Name:   name,
		Image:  image,
		Env:    env,
	})
	if err != nil {
		slog.ErrorContext(ctx, "summon failed", "bead", b.ID, "error", err)
		r.fail(ctx, b.ID, "summon: "+err.Error())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return false
	}
	slog.InfoContext(ctx, "vessel summoned", "bead", b.ID, "vessel", name, "container", v.ID)
	return true
}

// reap harvests an exited vessel: exit 0 closes the bead, nonzero fails it.
func (r *Reconciler) reap(ctx context.Context, v vessel.Vessel) {
	ctx, span := tracer().Start(ctx, "vessel.reap.harvest", trace.WithAttributes(
		attribute.String(telemetry.AttrBeadID, v.BeadID),
		attribute.String(telemetry.AttrVesselName, v.Name),
	))
	defer span.End()

	code, err := r.Vessels.Wait(ctx, v.ID)
	if err != nil {
		slog.ErrorContext(ctx, "exit harvest failed", "container", v.ID, "error", err)
		code = -1
	}
	span.SetAttributes(attribute.Int64("legion.vessel.exit_code", code))

	if v.BeadID != "" {
		if code == 0 {
			if err := r.Beads.Close(ctx, v.BeadID, fmt.Sprintf("vessel %s completed", v.Name)); err != nil {
				slog.ErrorContext(ctx, "close failed", "bead", v.BeadID, "error", err)
				span.SetStatus(codes.Error, err.Error())
				return // keep the container; retry next tick
			}
			slog.InfoContext(ctx, "bead closed", "bead", v.BeadID)
		} else {
			if err := r.Beads.Fail(ctx, v.BeadID, fmt.Sprintf("vessel %s exit %d", v.Name, code)); err != nil {
				slog.ErrorContext(ctx, "fail failed", "bead", v.BeadID, "error", err)
				span.SetStatus(codes.Error, err.Error())
				return
			}
			slog.WarnContext(ctx, "bead failed", "bead", v.BeadID, "exit", code)
		}
	}
	if err := r.Vessels.Reap(ctx, v.ID); err != nil {
		slog.ErrorContext(ctx, "reap failed", "container", v.ID, "error", err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// timeout kills a vessel that exceeded the per-bead deadline and fails
// its bead.
func (r *Reconciler) timeout(ctx context.Context, v vessel.Vessel) {
	ctx, span := tracer().Start(ctx, "vessel.timeout", trace.WithAttributes(
		attribute.String(telemetry.AttrBeadID, v.BeadID),
		attribute.String(telemetry.AttrVesselName, v.Name),
	))
	defer span.End()

	slog.WarnContext(ctx, "vessel timed out", "bead", v.BeadID, "container", v.ID)
	if v.BeadID != "" {
		r.fail(ctx, v.BeadID, fmt.Sprintf("timed out after %s", r.Config.Archon.BeadTimeout.Duration))
	}
	if err := r.Vessels.Reap(ctx, v.ID); err != nil {
		slog.ErrorContext(ctx, "reap failed", "container", v.ID, "error", err)
		span.SetStatus(codes.Error, err.Error())
	}
}

func (r *Reconciler) fail(ctx context.Context, id, reason string) {
	if err := r.Beads.Fail(ctx, id, reason); err != nil {
		slog.ErrorContext(ctx, "fail failed", "bead", id, "error", err)
	}
}

// Run ticks the reconciler until the context is cancelled.
func (r *Reconciler) Run(ctx context.Context) error {
	interval := r.Config.Archon.PollInterval.Duration
	slog.InfoContext(ctx, "archon running", "poll_interval", interval,
		"max_vessels", r.Config.Archon.MaxVessels)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.Tick(ctx); err != nil {
			slog.ErrorContext(ctx, "reconcile tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
