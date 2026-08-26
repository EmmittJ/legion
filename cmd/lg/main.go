package main

import (
	"context"
	"fmt"
	"os"

	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/telemetry"
	"github.com/EmmittJ/legion/internal/vessel"
)

const usage = `lg - Legion operator CLI

Usage:
  lg init                    prepare this repo (bd init + .legion/config.toml)
  lg invoke "task" [flags]   file a bead for Archon to work
  lg status [--json]         in-flight beads and their vessels
  lg log <bead-id> [-f]      stream a bead's vessel logs
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx := context.Background()
	shutdown, err := telemetry.Init(ctx, "lg")
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg: telemetry: %v\n", err)
	} else {
		defer shutdown(ctx)
	}

	a := &app{
		beads: bead.New(),
		vessels: func(ctx context.Context) (vesselAPI, error) {
			return vessel.New(ctx)
		},
		run: execCmd,
		out: os.Stdout,
	}

	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "init":
		err = a.cmdInit(".")
	case "invoke":
		err = a.cmdInvoke(ctx, args)
	case "status":
		err = a.cmdStatus(ctx, args)
	case "log":
		err = a.cmdLog(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "lg: unknown command %q\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg: %v\n", err)
		os.Exit(1)
	}
}
