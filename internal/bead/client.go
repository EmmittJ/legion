package bead

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/telemetry"
)

// runner executes a bd invocation and returns stdout. Injectable for tests.
type runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bd %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Client wraps the bd CLI. The zero value is not usable; use New.
type Client struct {
	dir string
	run runner
}

// Option configures a Client.
type Option func(*Client)

// WithDir sets the working directory for bd invocations (the repo whose
// .beads database is the source of truth).
func WithDir(dir string) Option { return func(c *Client) { c.dir = dir } }

// withRunner overrides command execution; test-only.
func withRunner(r runner) Option { return func(c *Client) { c.run = r } }

// New returns a Client that shells out to bd with --json.
func New(opts ...Option) *Client {
	c := &Client{run: execRunner}
	for _, o := range opts {
		o(c)
	}
	return c
}

func tracer() trace.Tracer { return otel.Tracer("legion/internal/bead") }

func (c *Client) span(ctx context.Context, op, beadID string) (context.Context, trace.Span) {
	ctx, span := tracer().Start(ctx, "bead."+op)
	if beadID != "" {
		span.SetAttributes(attribute.String(telemetry.AttrBeadID, beadID))
	}
	return ctx, span
}

func end(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func (c *Client) beads(ctx context.Context, args ...string) ([]Bead, error) {
	out, err := c.run(ctx, c.dir, append(args, "--json")...)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || string(out) == "null" {
		return nil, nil
	}
	var bs []Bead
	if err := json.Unmarshal(out, &bs); err != nil {
		// Some bd commands emit a single object rather than an array.
		var b Bead
		if err2 := json.Unmarshal(out, &b); err2 == nil && b.ID != "" {
			return []Bead{b}, nil
		}
		return nil, fmt.Errorf("parse bd output: %w", err)
	}
	return bs, nil
}

// Ready lists open beads whose dependencies are all satisfied.
func (c *Client) Ready(ctx context.Context) ([]Bead, error) {
	ctx, span := c.span(ctx, "ready", "")
	bs, err := c.beads(ctx, "ready")
	end(span, err)
	return bs, err
}

// Get fetches one bead by ID.
func (c *Client) Get(ctx context.Context, id string) (*Bead, error) {
	ctx, span := c.span(ctx, "get", id)
	bs, err := c.beads(ctx, "show", id)
	if err == nil && len(bs) == 0 {
		err = fmt.Errorf("bead %s not found", id)
	}
	end(span, err)
	if err != nil {
		return nil, err
	}
	return &bs[0], nil
}

// Claim atomically claims a bead (sets in_progress + assignee).
func (c *Client) Claim(ctx context.Context, id string) error {
	ctx, span := c.span(ctx, "claim", id)
	_, err := c.beads(ctx, "update", id, "--claim")
	end(span, err)
	return err
}

// Trace appends a progress comment to the bead — the audit trail of work.
func (c *Client) Trace(ctx context.Context, id, text string) error {
	ctx, span := c.span(ctx, "trace", id)
	_, err := c.run(ctx, c.dir, "comment", id, text, "--json")
	end(span, err)
	return err
}

// Close marks the bead completed. Only Archon may call this in production.
func (c *Client) Close(ctx context.Context, id, reason string) error {
	ctx, span := c.span(ctx, "close", id)
	_, err := c.beads(ctx, "close", id, "--reason", reason)
	end(span, err)
	return err
}

// Fail records a failure trace and returns the bead to open so it can be
// retried or triaged. Only Archon may call this in production.
func (c *Client) Fail(ctx context.Context, id, reason string) error {
	ctx, span := c.span(ctx, "fail", id)
	err := c.Trace(ctx, id, "FAILED: "+reason)
	if err == nil {
		_, err = c.beads(ctx, "update", id, "--status", StatusOpen)
	}
	end(span, err)
	return err
}

// CreateOpts describes a new bead.
type CreateOpts struct {
	Description    string
	IssueType      string // bug, feature, task, epic, chore
	Priority       int    // 0..4
	Labels         []string
	DiscoveredFrom string // parent bead ID for discovered work
}

// Create files a new bead and returns it.
func (c *Client) Create(ctx context.Context, title string, opts CreateOpts) (*Bead, error) {
	ctx, span := c.span(ctx, "create", "")
	args := []string{"create", title, "-p", strconv.Itoa(opts.Priority)}
	if opts.Description != "" {
		args = append(args, "-d", opts.Description)
	}
	if opts.IssueType != "" {
		args = append(args, "-t", opts.IssueType)
	}
	if len(opts.Labels) > 0 {
		args = append(args, "-l", strings.Join(opts.Labels, ","))
	}
	if opts.DiscoveredFrom != "" {
		args = append(args, "--deps", "discovered-from:"+opts.DiscoveredFrom)
	}
	bs, err := c.beads(ctx, args...)
	if err == nil && len(bs) == 0 {
		err = fmt.Errorf("bd create returned no bead")
	}
	if err == nil {
		span.SetAttributes(attribute.String(telemetry.AttrBeadID, bs[0].ID))
	}
	end(span, err)
	if err != nil {
		return nil, err
	}
	return &bs[0], nil
}

// Children lists child beads of a parent.
func (c *Client) Children(ctx context.Context, id string) ([]Bead, error) {
	ctx, span := c.span(ctx, "children", id)
	bs, err := c.beads(ctx, "children", id)
	end(span, err)
	return bs, err
}

// DoltPull syncs bead state down from the git remote. Vessels call this at
// possession start.
func (c *Client) DoltPull(ctx context.Context) error {
	ctx, span := c.span(ctx, "dolt_pull", "")
	_, err := c.run(ctx, c.dir, "dolt", "pull")
	end(span, err)
	return err
}

// DoltPush publishes bead state to the git remote.
func (c *Client) DoltPush(ctx context.Context) error {
	ctx, span := c.span(ctx, "dolt_push", "")
	_, err := c.run(ctx, c.dir, "dolt", "push")
	end(span, err)
	return err
}
