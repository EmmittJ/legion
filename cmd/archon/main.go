// Archon is Legion's reconciler daemon: it summons vessels for ready
// beads and reaps finished ones. Run it next to the operating repo
// (where .legion/config.toml and the Beads database live).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/EmmittJ/legion/internal/archon"
	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/config"
	"github.com/EmmittJ/legion/internal/telemetry"
	"github.com/EmmittJ/legion/internal/vessel"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "archon:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to .legion/config.toml (default: search upward from cwd)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "archon")
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

	path := *configPath
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		if path, err = config.Find(cwd); err != nil {
			return err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	// The Beads database lives in the operating repo, two levels up from
	// .legion/config.toml.
	repoRoot := filepath.Dir(filepath.Dir(path))

	vessels, err := vessel.New(ctx)
	if err != nil {
		return err
	}

	// Forward credentials and the collector endpoint into vessels.
	env := map[string]string{}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}

	r := &archon.Reconciler{
		Beads:   bead.New(bead.WithDir(repoRoot)),
		Vessels: vessels,
		Config:  cfg,
		Env:     env,
	}
	err = r.Run(ctx)
	if err == context.Canceled {
		slog.Info("archon stopped")
		return nil
	}
	return err
}
