package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

//go:embed pacts/*
var pactFS embed.FS

// version is set at build time via -ldflags "-X main.version=<value>".
// Falls back to "dev" when not injected.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "--version", "-version":
		fmt.Println(version)
	case "init":
		cmdInit()
	case "invoke":
		cmdInvoke()
	case "status":
		cmdStatus()
	case "log":
		cmdLog()
	case "watch":
		cmdWatch()
	default:
		fmt.Fprintf(os.Stderr, "lg: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  lg --version                            — print the binary version string")
	fmt.Fprintln(os.Stderr, "  lg init                                 — scaffold a Legion workspace in the current repo")
	fmt.Fprintln(os.Stderr, "  lg invoke \"<title>\" [--agent <name>] [--model <tier|name>]  — create a new task issue; optionally")
	fmt.Fprintln(os.Stderr, "                                           route to a known agent by name or pin a model tier/ID")
	fmt.Fprintln(os.Stderr, "  lg status                               — list open and in-progress issues")
	fmt.Fprintln(os.Stderr, "  lg log <issue-id> [--follow]            — print ACP execution traces for an issue;")
	fmt.Fprintln(os.Stderr, "                                           --follow polls every 2s and tails new lines")
	fmt.Fprintln(os.Stderr, "  lg watch [--interval=N]                 — live-refreshing status dashboard (default: 3s)")
}

// ── cmdInit ───────────────────────────────────────────────────────────────────

// cmdInit scaffolds a Legion workspace in the current repo.
// It is idempotent: prints "Created:" or "Exists:" for each artifact.
// Never overwrites existing files.
//
//	lg init
func cmdInit() {
	// ── 1. Role beads ────────────────────────────────────────────────────────
	rolesToCreate := []struct {
		title       string
		description string
	}{
		{"worker", "Worker vessel: writes code on a branch, commits, pushes PR"},
		{"oracle", "Oracle vessel: human-facing intake; gathers intent from Summoner and creates beads"},
		{"hermes", "Hermes vessel: routing vessel; reads a bead and emits a role label (one bead in, one decision out)"},
		{"hierophant", "Hierophant vessel: expands vague intent into dependency graph of issues"},
		{"inquisitor", "Inquisitor vessel: peer reviews code and runs CI; delivers pass/fail verdict"},
		{"weaver", "Weaver vessel: merges approved branches after inquisitor sign-off"},
	}

	// Discover roles that already exist so we don't create duplicates.
	existingRoles := map[string]bool{}
	if out, err := bdOutput("list", "--type=role", "--json"); err == nil {
		var items []struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(out, &items) == nil {
			for _, it := range items {
				existingRoles[it.Title] = true
			}
		}
	}

	for _, role := range rolesToCreate {
		if existingRoles[role.title] {
			fmt.Printf("Exists:  role:%s\n", role.title)
			continue
		}
		if _, err := bdOutput("create", role.title, "--type=role",
			"--description="+role.description, "--json"); err != nil {
			fmt.Fprintf(os.Stderr, "lg init: create role %s: %v\n", role.title, err)
		} else {
			fmt.Printf("Created: role:%s\n", role.title)
		}
	}

	// ── 2. Config files (.legion/) ───────────────────────────────────────────
	root, err := gitRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg init: could not determine repo root: %v\n", err)
		os.Exit(1)
	}

	legionDir := filepath.Join(root, ".legion")
	if err := os.MkdirAll(legionDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lg init: mkdir .legion: %v\n", err)
		os.Exit(1)
	}

	if err := fs.WalkDir(pactFS, "pacts/config", func(embPath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := pactFS.ReadFile(embPath)
		writeScaffoldFile(root, filepath.Join(legionDir, filepath.Base(embPath)), data)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "lg init: walk pacts/config: %v\n", err)
	}
	writeScaffoldFile(root, filepath.Join(legionDir, ".gitkeep"), []byte{})

	// ── 3. .gitignore ─────────────────────────────────────────────────────────
	appendLineIfAbsent(root, filepath.Join(root, ".gitignore"), ".legion/context/")

	// ── 4. Agent pacts (.github/agents/) ─────────────────────────────────────
	agentsDir := filepath.Join(root, ".github", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lg init: mkdir .github/agents: %v\n", err)
		os.Exit(1)
	}

	if err := fs.WalkDir(pactFS, "pacts/agents", func(embPath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := pactFS.ReadFile(embPath)
		writeScaffoldFile(root, filepath.Join(agentsDir, filepath.Base(embPath)), data)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "lg init: walk pacts/agents: %v\n", err)
	}

	// ── 5. Skills (.github/skills/) ───────────────────────────────────────────
	const skillsPrefix = "pacts/skills/"
	skillsDest := filepath.Join(root, ".github", "skills")

	if err := fs.WalkDir(pactFS, "pacts/skills", func(embPath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(embPath, skillsPrefix)
		dest := filepath.Join(skillsDest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "lg init: mkdir %s: %v\n", filepath.Dir(dest), err)
			return nil
		}
		data, _ := pactFS.ReadFile(embPath)
		writeScaffoldFile(root, dest, data)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "lg init: walk pacts/skills: %v\n", err)
	}
}

// writeScaffoldFile writes content to path only if the file does not already
// exist. Prints "Created: <rel>" or "Exists:  <rel>" relative to repoRoot.
func writeScaffoldFile(repoRoot, path string, content []byte) {
	rel := scaffoldRel(repoRoot, path)
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Exists:  %s\n", rel)
		return
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "lg init: write %s: %v\n", rel, err)
		return
	}
	fmt.Printf("Created: %s\n", rel)
}

// appendLineIfAbsent appends line to path (creating the file if needed) unless
// the line is already present. Prints "Updated:" or "Exists:" accordingly.
func appendLineIfAbsent(repoRoot, path, line string) {
	rel := scaffoldRel(repoRoot, path)
	existing, readErr := os.ReadFile(path)
	if readErr == nil && strings.Contains(string(existing), line) {
		fmt.Printf("Exists:  %s (%s)\n", rel, line)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg init: update %s: %v\n", rel, err)
		return
	}
	defer f.Close()
	// Ensure the new entry starts on its own line.
	prefix := "\n"
	if readErr == nil && len(existing) > 0 && existing[len(existing)-1] == '\n' {
		prefix = ""
	}
	fmt.Fprintf(f, "%s%s\n", prefix, line)
	fmt.Printf("Updated: %s (+%s)\n", rel, line)
}

// scaffoldRel returns path relative to repoRoot for display; falls back to the
// full path on error.
func scaffoldRel(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// lg invoke "Fix the login bug" [--agent <name>] [--model <tier|name>]
func cmdInvoke() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "lg invoke: title required")
		fmt.Fprintln(os.Stderr, "  usage: lg invoke \"<title>\" [--agent <name>] [--model <tier|name>]")
		os.Exit(1)
	}
	title := os.Args[2]

	// Parse --agent <name> or --agent=<name> and --model <value> or --model=<value> from remaining args.
	agentName := ""
	modelValue := ""
	rest := os.Args[3:]
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--agent":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "lg invoke: --agent requires a value")
				fmt.Fprintln(os.Stderr, "  usage: lg invoke \"<title>\" --agent <name>")
				os.Exit(1)
			}
			agentName = rest[i+1]
			i++ // consume value
		case strings.HasPrefix(rest[i], "--agent="):
			agentName = strings.TrimPrefix(rest[i], "--agent=")
		case rest[i] == "--model":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "lg invoke: --model requires a value")
				fmt.Fprintln(os.Stderr, "  usage: lg invoke \"<title>\" --model <tier|name>")
				os.Exit(1)
			}
			modelValue = rest[i+1]
			i++ // consume value
		case strings.HasPrefix(rest[i], "--model="):
			modelValue = strings.TrimPrefix(rest[i], "--model=")
		default:
			fmt.Fprintf(os.Stderr, "lg invoke: unknown flag %q\n", rest[i])
			os.Exit(1)
		}
	}

	// Validate agent BEFORE touching Beads.
	if agentName != "" {
		known, err := discoverAgents()
		if err != nil {
			fmt.Fprintf(os.Stderr, "lg invoke: could not discover agents: %v\n", err)
			os.Exit(1)
		}
		if !slices.Contains(known, agentName) {
			fmt.Fprintf(os.Stderr, "lg invoke: unknown agent %q\n", agentName)
			fmt.Fprintf(os.Stderr, "  known agents: %s\n", strings.Join(known, ", "))
			os.Exit(1)
		}
	}

	// Build the bd create arg list.
	bdArgs := []string{"create", title, "--type=task", "--description=" + title}
	bdArgs = append(bdArgs, "--labels", "dispatch:auto") // marks issue for Archon auto-dispatch
	if agentName != "" {
		bdArgs = append(bdArgs, "--labels", "agent:"+agentName)
	}
	if modelValue != "" {
		bdArgs = append(bdArgs, "--labels", "model:"+modelValue)
	}
	bdArgs = append(bdArgs, "--json")

	out, err := bdOutput(bdArgs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg invoke: %v\n", err)
		os.Exit(1)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		// bd create output format may vary — try to print whatever we got.
		fmt.Printf("Created issue: %s\n", strings.TrimSpace(string(out)))
		return
	}

	fmt.Printf("Created issue: %s\n", result.ID)
}

// cmdStatus lists open and in-progress issues as a table.
//
//	lg status
func cmdStatus() {
	type issueCore struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Assignee string `json:"assignee"`
	}

	var allIssues []issueCore

	for _, status := range []string{"open", "in_progress"} {
		out, err := bdOutput("list", "--status="+status, "--flat", "--json")
		if err != nil {
			fmt.Fprintf(os.Stderr, "lg status: bd list --status=%s: %v\n", status, err)
			continue
		}

		var batch []issueCore
		if err := json.Unmarshal(out, &batch); err != nil {
			fmt.Fprintf(os.Stderr, "lg status: parse %s issues: %v\n", status, err)
			continue
		}
		for _, e := range batch {
			allIssues = append(allIssues, e)
		}
	}

	if len(allIssues) == 0 {
		fmt.Println("No open or in-progress issues.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tASSIGNED_TO")
	fmt.Fprintln(w, "──\t─────\t──────\t───────────")
	for _, iss := range allIssues {
		assignedTo := iss.Assignee
		if assignedTo == "" {
			assignedTo = "—"
		}
		title := iss.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", iss.ID, title, iss.Status, assignedTo)
	}
	w.Flush()
}

// cmdLog prints the ACP execution traces (notes field) for a single issue.
//
//	lg log <issue-id> [--follow]
//
// Without --follow: fetch once, print, exit.
// With --follow: print existing lines, then poll every 2 s and tail new lines
// (detected by line count). Exits cleanly on SIGINT / SIGTERM.
func cmdLog() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "lg log: issue-id required")
		fmt.Fprintln(os.Stderr, "  usage: lg log <issue-id> [--follow]")
		os.Exit(1)
	}
	issueID := os.Args[2]

	// Parse --follow / -f from any remaining arg.
	follow := false
	for _, arg := range os.Args[3:] {
		if arg == "--follow" || arg == "-f" {
			follow = true
		}
	}

	type issueDetail struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		Description string `json:"description"`
		Assignee    string `json:"assignee"`
		Notes       string `json:"notes"`
	}

	// fetchDetail calls bd show <id> --json and returns the parsed issue.
	fetchDetail := func() (issueDetail, error) {
		out, err := bdOutput("show", issueID, "--json")
		if err != nil {
			return issueDetail{}, err
		}
		// bd show returns a single-element flat array: [{"id":"...","title":"...",...}]
		var items []issueDetail
		if jsonErr := json.Unmarshal(out, &items); jsonErr != nil || len(items) == 0 {
			// Fallback: surface raw output so the caller can decide.
			return issueDetail{}, fmt.Errorf("parse response: %w (raw: %s)", jsonErr, strings.TrimSpace(string(out)))
		}
		return items[0], nil
	}

	// splitLines trims a trailing newline before splitting so that notes ending
	// in "\n" don't produce a phantom empty element that would skew line counts.
	splitLines := func(s string) []string {
		return strings.Split(strings.TrimRight(s, "\n"), "\n")
	}

	// printFrom prints lines[from:] to stdout, one per line, and returns the
	// new total line count.
	printFrom := func(lines []string, from int) int {
		for i := from; i < len(lines); i++ {
			fmt.Println(lines[i])
		}
		return len(lines)
	}

	// ── Initial fetch ─────────────────────────────────────────────────────────
	detail, err := fetchDetail()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg log: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Issue: %s — %s [%s]\n", detail.ID, detail.Title, detail.Status)
	if detail.Description != "" {
		fmt.Printf("Description: %s\n", detail.Description)
	}
	if detail.Assignee != "" {
		fmt.Printf("Assigned to: %s\n", detail.Assignee)
	}
	fmt.Println()

	seenLines := 0
	if detail.Notes == "" {
		if !follow {
			fmt.Println("(no traces)")
			return
		}
		fmt.Println("(no traces yet — following...)")
	} else {
		lines := splitLines(detail.Notes)
		seenLines = printFrom(lines, 0)
	}

	if !follow {
		return
	}

	// ── Follow mode ───────────────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return
		case <-ticker.C:
			d, pollErr := fetchDetail()
			if pollErr != nil {
				// Don't exit — bd may be temporarily unavailable. Log and retry.
				fmt.Fprintf(os.Stderr, "lg log: poll: %v\n", pollErr)
				continue
			}
			if d.Notes != "" {
				lines := splitLines(d.Notes)
				seenLines = printFrom(lines, seenLines)
			}
		}
	}
}

// cmdWatch runs a live-refreshing terminal dashboard that polls Beads every N
// seconds and reprints a status summary. Exits cleanly on SIGINT / SIGTERM.
//
//	lg watch [--interval=<seconds>]
func cmdWatch() {
	interval := 3 // default

	// Parse --interval=N from remaining args (os.Args[2:]).
	for _, arg := range os.Args[2:] {
		if strings.HasPrefix(arg, "--interval=") {
			val := strings.TrimPrefix(arg, "--interval=")
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "lg watch: invalid --interval %q (must be a positive integer)\n", val)
				os.Exit(1)
			}
			interval = n
		}
	}

	// Set up signal handling so Ctrl+C exits cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	type issueCore struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Assignee string `json:"assignee"`
	}

	// truncate returns s truncated to at most n runes, with "…" appended when
	// trimming occurs.
	truncate := func(s string, n int) string {
		runes := []rune(s)
		if len(runes) <= n {
			return s
		}
		return string(runes[:n-1]) + "…"
	}

	// fetchIssues calls bd list for a single status and returns parsed results.
	// On any error it returns nil, signalling the section should show
	// "(unavailable)".
	fetchIssues := func(status string) ([]issueCore, bool) {
		out, err := bdOutput("list", "--status="+status, "--flat", "--json")
		if err != nil {
			return nil, false
		}
		var batch []issueCore
		if err := json.Unmarshal(out, &batch); err != nil {
			return nil, false
		}
		return batch, true
	}

	paint := func() {
		// Clear screen and move cursor to top.
		fmt.Print("\033[H\033[2J")

		ts := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
		header := fmt.Sprintf("║  Legion Watch  —  %s", ts)
		// Pad to fixed width (54 printable chars inside the box).
		const boxInner = 54
		pad := boxInner - len([]rune(header)) + 1 // +1 for leading ║
		if pad < 0 {
			pad = 0
		}
		fmt.Println("╔══════════════════════════════════════════════════════╗")
		fmt.Printf("%s%s║\n", header, strings.Repeat(" ", pad))
		fmt.Println("╚══════════════════════════════════════════════════════╝")
		fmt.Println()

		// ── ACTIVE ────────────────────────────────────────────────────────
		active, activeOK := fetchIssues("in_progress")
		fmt.Println("ACTIVE  (in_progress)")
		switch {
		case !activeOK:
			fmt.Println("  (unavailable)")
		case len(active) == 0:
			fmt.Println("  (none)")
		default:
			for _, iss := range active {
				assignedTo := iss.Assignee
				if assignedTo == "" {
					assignedTo = "—"
				}
				fmt.Printf("  %-10s  %-42s  %s\n",
					iss.ID,
					truncate(iss.Title, 40),
					assignedTo,
				)
			}
		}
		fmt.Println()

		// ── READY ─────────────────────────────────────────────────────────
		ready, readyOK := fetchIssues("open")
		switch {
		case !readyOK:
			fmt.Println("READY  (unavailable)")
			fmt.Println("  (unavailable)")
		case len(ready) == 0:
			fmt.Println("READY  (0 waiting)")
			fmt.Println("  (none)")
		default:
			fmt.Printf("READY  (%d waiting)\n", len(ready))
			for _, iss := range ready {
				fmt.Printf("  %-10s  %s\n", iss.ID, truncate(iss.Title, 40))
			}
		}
		fmt.Println()

		// ── RECENT ────────────────────────────────────────────────────────
		var recent []issueCore
		recentOK := true
		for _, st := range []string{"closed", "failed"} {
			batch, ok := fetchIssues(st)
			if !ok {
				recentOK = false
				break
			}
			recent = append(recent, batch...)
		}
		fmt.Println("RECENT  (closed / failed — last 5)")
		switch {
		case !recentOK:
			fmt.Println("  (unavailable)")
		case len(recent) == 0:
			fmt.Println("  (none yet)")
		default:
			// Take the last 5 by array order.
			start := len(recent) - 5
			if start < 0 {
				start = 0
			}
			for _, iss := range recent[start:] {
				fmt.Printf("  %-10s  %-42s  [%s]\n",
					iss.ID,
					truncate(iss.Title, 40),
					iss.Status,
				)
			}
		}
		fmt.Println()

		fmt.Printf("Press Ctrl+C to exit  |  refreshing every %ds\n", interval)
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	// Paint immediately before first tick.
	paint()

	for {
		select {
		case <-sigCh:
			fmt.Println("\nExiting.")
			return
		case <-ticker.C:
			paint()
		}
	}
}

// gitRoot returns the absolute path to the repository root by running
// git rev-parse --show-toplevel.
func gitRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// discoverAgents returns the list of agent names found in
// <repo-root>/.github/agents/*.agent.md.
func discoverAgents() ([]string, error) {
	root, err := gitRoot()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(root, ".github", "agents", "*.agent.md"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range matches {
		base := filepath.Base(m)
		names = append(names, strings.TrimSuffix(base, ".agent.md"))
	}
	return names, nil
}

// bdOutput runs a bd subcommand and returns stdout.
func bdOutput(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bd", args...)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		// If context timed out but we got output, the command wrote its result
		// before hanging on auto-push — treat as success.
		if ctx.Err() == context.DeadlineExceeded && len(out) > 0 {
			return out, nil
		}
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("bd %s: %w\nstderr: %s", strings.Join(args, " "), err, stderr.String())
		}
		return nil, fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
