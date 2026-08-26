// lg is Legion's operator CLI: init, invoke, status, log.
// bd is for the machinery; lg is for humans.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/moby/moby/api/pkg/stdcopy"

	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/vessel"
)

// beadAPI is the slice of bead.Client lg needs.
type beadAPI interface {
	Create(ctx context.Context, title string, opts bead.CreateOpts) (*bead.Bead, error)
	List(ctx context.Context, status string) ([]bead.Bead, error)
}

// vesselAPI is the slice of vessel.Manager lg needs.
type vesselAPI interface {
	List(ctx context.Context) ([]vessel.Vessel, error)
	Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error)
}

// runner executes an external command in dir, returning combined output.
type runner func(dir, name string, args ...string) (string, error)

func execCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// app wires lg's commands to their dependencies so tests can fake them.
type app struct {
	beads   beadAPI
	vessels func(ctx context.Context) (vesselAPI, error) // lazy: docker optional
	run     runner
	out     io.Writer
}

const configTemplate = `# Legion operator configuration. See docs/architecture.md.

# Git URL of the repo Legion works on (cloned inside each vessel).
repo_url = %q

# Vessel used when a bead has no vessel: label.
default_vessel = "copilot"

# Vessel registry: name -> container image with the harness baked in.
[vessels]
copilot = "legion/vessel-copilot:latest"

[archon]
poll_interval = "5s"
max_vessels = 3
bead_timeout = "30m"
`

// cmdInit prepares a repo for Legion: bd init + .legion/config.toml.
func (a *app) cmdInit(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".beads")); os.IsNotExist(err) {
		if _, err := a.run(dir, "bd", "init"); err != nil {
			return fmt.Errorf("bd init: %w", err)
		}
		fmt.Fprintln(a.out, "initialized beads database (.beads)")
	} else {
		fmt.Fprintln(a.out, "beads database already present")
	}

	cfgPath := filepath.Join(dir, ".legion", "config.toml")
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Fprintln(a.out, ".legion/config.toml already present")
		return nil
	}
	repoURL := ""
	if out, err := a.run(dir, "git", "remote", "get-url", "origin"); err == nil {
		repoURL = strings.TrimSpace(out)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, fmt.Appendf(nil, configTemplate, repoURL), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "wrote .legion/config.toml")
	if repoURL == "" {
		fmt.Fprintln(a.out, "note: set repo_url in .legion/config.toml (no git origin found)")
	}
	return nil
}

// invokeOpts builds CreateOpts from invoke flags; routing rides as labels.
func invokeOpts(desc, issueType string, priority int, vesselName, persona string) bead.CreateOpts {
	var labels []string
	if vesselName != "" {
		labels = append(labels, "vessel:"+vesselName)
	}
	if persona != "" {
		labels = append(labels, "persona:"+persona)
	}
	return bead.CreateOpts{Description: desc, IssueType: issueType, Priority: priority, Labels: labels}
}

// cmdInvoke files a bead for Archon to pick up.
func (a *app) cmdInvoke(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("invoke", flag.ContinueOnError)
	desc := fs.String("d", "", "bead description")
	issueType := fs.String("t", "task", "issue type: bug, feature, task, epic, chore")
	priority := fs.Int("p", 2, "priority 0 (critical) to 4 (backlog)")
	vesselName := fs.String("vessel", "", "vessel to summon (default from .legion/config.toml)")
	persona := fs.String("persona", "", "persona in the target repo for the harness to load")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.SetOutput(a.out)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: lg invoke \"task title\" [-d desc] [-p 0-4] [-t type] [--vessel name] [--persona name]")
	}

	b, err := a.beads.Create(ctx, fs.Arg(0), invokeOpts(*desc, *issueType, *priority, *vesselName, *persona))
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(a.out).Encode(b)
	}
	fmt.Fprintf(a.out, "invoked %s: %s\n", b.ID, b.Title)
	if v := b.Vessel(); v != "" {
		fmt.Fprintf(a.out, "  vessel:  %s\n", v)
	}
	if p := b.Persona(); p != "" {
		fmt.Fprintf(a.out, "  persona: %s\n", p)
	}
	fmt.Fprintln(a.out, "Archon will summon a vessel on its next tick.")
	return nil
}

// statusReport is the --json shape of lg status.
type statusReport struct {
	Beads   []bead.Bead     `json:"beads"`
	Vessels []vessel.Vessel `json:"vessels"`
}

// cmdStatus shows in-flight beads and the vessels working them.
func (a *app) cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.SetOutput(a.out)
	if err := fs.Parse(args); err != nil {
		return err
	}

	var report statusReport
	for _, status := range []string{"open", "in_progress"} {
		bs, err := a.beads.List(ctx, status)
		if err != nil {
			return err
		}
		report.Beads = append(report.Beads, bs...)
	}
	dockerErr := error(nil)
	if vm, err := a.vessels(ctx); err != nil {
		dockerErr = err
	} else if report.Vessels, err = vm.List(ctx); err != nil {
		dockerErr = err
	}

	if *asJSON {
		return json.NewEncoder(a.out).Encode(report)
	}

	byBead := map[string]*vessel.Vessel{}
	for i := range report.Vessels {
		byBead[report.Vessels[i].BeadID] = &report.Vessels[i]
	}
	tw := tabwriter.NewWriter(a.out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BEAD\tSTATUS\tP\tVESSEL\tTITLE")
	for _, b := range report.Beads {
		vs := "-"
		if v, ok := byBead[b.ID]; ok {
			vs = fmt.Sprintf("%s (%s)", v.Name, v.State)
		} else if want := b.Vessel(); want != "" {
			vs = want + " (pending)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", b.ID, b.Status, b.Priority, vs, b.Title)
	}
	tw.Flush()
	if len(report.Beads) == 0 {
		fmt.Fprintln(a.out, "no open or in-progress beads")
	}
	if dockerErr != nil {
		fmt.Fprintf(a.out, "warning: vessel state unavailable: %v\n", dockerErr)
	}
	return nil
}

// cmdLog streams the vessel logs for a bead.
func (a *app) cmdLog(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	follow := fs.Bool("f", false, "follow log output")
	fs.SetOutput(a.out)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: lg log <bead-id> [-f]")
	}
	beadID := fs.Arg(0)

	vm, err := a.vessels(ctx)
	if err != nil {
		return fmt.Errorf("docker unavailable: %w", err)
	}
	vs, err := vm.List(ctx)
	if err != nil {
		return err
	}
	var target *vessel.Vessel
	for i := range vs {
		if vs[i].BeadID == beadID {
			target = &vs[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no vessel found for bead %s (already reaped, or never summoned)", beadID)
	}
	rc, err := vm.Logs(ctx, target.ID, *follow)
	if err != nil {
		return err
	}
	defer rc.Close()
	// Vessel logs are Docker stream-multiplexed; demux both to out.
	_, err = stdcopy.StdCopy(a.out, a.out, rc)
	return err
}
