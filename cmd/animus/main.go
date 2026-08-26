// Animus is Legion's in-vessel driver: it possesses the vessel, drives
// the harness over ACP, and (re-invoked as `animus mcp`) serves Legion's
// bead tools to the working model over MCP stdio.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/EmmittJ/legion/internal/acp"
	"github.com/EmmittJ/legion/internal/animus"
	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "animus:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Join the bead's trace started by Archon.
	ctx = telemetry.ExtractEnv(ctx, os.Getenv)

	shutdown, err := telemetry.Init(ctx, "animus")
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(sctx); err != nil {
			slog.Error("telemetry shutdown", "error", err)
		}
	}()

	workDir := os.Getenv("LEGION_WORK_DIR")
	if workDir == "" {
		workDir = "/work"
	}
	beads := bead.New(bead.WithDir(workDir))

	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		// MCP server mode: spawned by the harness over stdio.
		beadID := os.Getenv(animus.EnvBeadID)
		if beadID == "" {
			return fmt.Errorf("%s must be set", animus.EnvBeadID)
		}
		return animus.MCPServer(beads, beadID).Run(ctx, &mcp.StdioTransport{})
	}

	// Possession mode: the vessel entrypoint. Validate the env contract
	// before touching the network.
	for _, key := range []string{animus.EnvBeadID, animus.EnvRepoURL, animus.EnvHarnessCmd} {
		if os.Getenv(key) == "" {
			return fmt.Errorf("%s must be set (see docs/architecture.md env contract)", key)
		}
	}
	if err := beads.Bootstrap(ctx, os.Getenv(animus.EnvRepoURL)); err != nil {
		return fmt.Errorf("beads bootstrap: %w", err)
	}

	selfExe, err := os.Executable()
	if err != nil {
		return err
	}

	return animus.Possess(ctx, animus.Deps{
		Beads: beads,
		RunGit: func(ctx context.Context, dir string, args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("git %v: %w: %s", args, err, out)
			}
			return string(out), nil
		},
		StartSession: func(ctx context.Context, cfg acp.Config) (animus.Session, error) {
			return acp.Start(ctx, cfg)
		},
		Lookup:  os.Getenv,
		WorkDir: workDir,
		SelfExe: selfExe,
	})
}
