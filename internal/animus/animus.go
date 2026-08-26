// Package animus implements Legion's in-vessel driver: the spirit that
// possesses a vessel. It reads its bead, clones the target repo,
// branches, drives the harness over ACP (injecting its own MCP tools),
// and pushes the result. See ADR-0005.
package animus

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/acp"
	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/telemetry"
)

// Environment contract between Archon, the vessel image, and the animus.
const (
	EnvBeadID      = "LEGION_BEAD_ID"      // set by Archon at summon
	EnvRepoURL     = "LEGION_REPO_URL"     // set by Archon at summon
	EnvHarnessCmd  = "HARNESS_CMD"         // baked into the vessel image, e.g. "copilot --stdio"
	EnvPersonaFlag = "HARNESS_PERSONA_FLAG" // baked into the vessel image, e.g. "--agent"
)

// BranchPrefix is where vessels push their work.
const BranchPrefix = "legion/"

// beadAPI is the slice of the bead layer the animus uses. *bead.Client
// satisfies it; tests fake it.
type beadAPI interface {
	Get(ctx context.Context, id string) (*bead.Bead, error)
	Trace(ctx context.Context, id, text string) error
	DoltPush(ctx context.Context) error
}

// Session is the slice of the ACP session the animus drives.
// *acp.Session satisfies it; tests fake it.
type Session interface {
	Prompt(ctx context.Context, text string) (acpsdk.StopReason, error)
	Close() error
}

// Deps wires the possession flow. Everything is injectable for tests.
type Deps struct {
	Beads beadAPI
	// RunGit runs git with args in dir and returns combined output.
	RunGit func(ctx context.Context, dir string, args ...string) (string, error)
	// StartSession launches the harness and opens an ACP session.
	StartSession func(ctx context.Context, cfg acp.Config) (Session, error)
	// Lookup reads the environment (os.Getenv in production).
	Lookup func(string) string
	// WorkDir is where the target repo is cloned (e.g. /work).
	WorkDir string
	// SelfExe is the animus binary path, re-invoked as the MCP server.
	SelfExe string
}

func tracer() trace.Tracer { return otel.Tracer("legion/internal/animus") }

// Possess runs the full in-vessel flow for one bead. A nil return means
// the vessel may exit 0 (Archon will close the bead).
func Possess(ctx context.Context, d Deps) error {
	beadID := d.Lookup(EnvBeadID)
	repoURL := d.Lookup(EnvRepoURL)
	if beadID == "" || repoURL == "" {
		return fmt.Errorf("animus: %s and %s must be set", EnvBeadID, EnvRepoURL)
	}

	ctx, span := tracer().Start(ctx, "animus.possess", trace.WithAttributes(
		attribute.String(telemetry.AttrBeadID, beadID),
	))
	defer span.End()
	fail := func(err error) error {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	b, err := d.Beads.Get(ctx, beadID)
	if err != nil {
		return fail(fmt.Errorf("animus: read bead: %w", err))
	}

	branch := BranchPrefix + b.ID
	span.SetAttributes(
		attribute.String(telemetry.AttrPersona, b.Persona()),
		attribute.String(telemetry.AttrBranch, branch),
	)

	repoDir := filepath.Join(d.WorkDir, "repo")
	if _, err := d.RunGit(ctx, d.WorkDir, "clone", repoURL, repoDir); err != nil {
		return fail(fmt.Errorf("animus: clone: %w", err))
	}
	if _, err := d.RunGit(ctx, repoDir, "checkout", "-b", branch); err != nil {
		return fail(fmt.Errorf("animus: branch: %w", err))
	}

	command, err := harnessCommand(d.Lookup, b.Persona())
	if err != nil {
		return fail(err)
	}
	span.SetAttributes(attribute.String(telemetry.AttrHarness, command[0]))

	_ = d.Beads.Trace(ctx, b.ID, fmt.Sprintf("animus: possessing vessel (harness %s, branch %s)", command[0], branch))

	sess, err := d.StartSession(ctx, acp.Config{
		Command: command,
		Cwd:     repoDir,
		McpServers: []acpsdk.McpServer{
			acp.StdioMcpServer("legion", d.SelfExe, []string{"mcp"}, map[string]string{
				EnvBeadID: b.ID,
			}),
		},
		OnUpdate: logUpdates(),
	})
	if err != nil {
		return fail(fmt.Errorf("animus: start session: %w", err))
	}
	defer sess.Close()

	stop, err := sess.Prompt(ctx, b.Prompt())
	if err != nil {
		return fail(fmt.Errorf("animus: prompt turn: %w", err))
	}
	if stop != acpsdk.StopReasonEndTurn {
		_ = d.Beads.Trace(ctx, b.ID, "animus: turn aborted: "+string(stop))
		return fail(fmt.Errorf("animus: harness stopped with %q", stop))
	}

	if _, err := d.RunGit(ctx, repoDir, "push", "origin", branch); err != nil {
		return fail(fmt.Errorf("animus: push: %w", err))
	}

	_ = d.Beads.Trace(ctx, b.ID, "animus: work pushed to "+branch)
	if err := d.Beads.DoltPush(ctx); err != nil {
		slog.WarnContext(ctx, "bd dolt push failed", "error", err)
	}
	return nil
}

// harnessCommand builds the harness argv from the image contract:
// HARNESS_CMD plus, when a persona is routed and the image declares a
// persona flag, that flag and the persona name. Legion never resolves
// the persona itself — the harness does.
func harnessCommand(lookup func(string) string, persona string) ([]string, error) {
	command := strings.Fields(lookup(EnvHarnessCmd))
	if len(command) == 0 {
		return nil, fmt.Errorf("animus: %s must be set by the vessel image", EnvHarnessCmd)
	}
	if persona != "" {
		if flag := lookup(EnvPersonaFlag); flag != "" {
			command = append(command, flag, persona)
		}
	}
	return command, nil
}

// logUpdates streams harness output into correlated logs.
func logUpdates() acp.UpdateHandler {
	return func(ctx context.Context, n acpsdk.SessionNotification) {
		u := n.Update
		switch {
		case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
			slog.InfoContext(ctx, "harness", "text", u.AgentMessageChunk.Content.Text.Text)
		case u.ToolCall != nil:
			slog.InfoContext(ctx, "harness tool call", "title", u.ToolCall.Title)
		}
	}
}
