package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
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
	case "doctor":
		cmdDoctor()
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
	fmt.Fprintln(os.Stderr, "  lg doctor                               — validate the full Legion stack")
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

// ── cmdDoctor ─────────────────────────────────────────────────────────────────

// doctorStatus is the outcome of a single health check.
type doctorStatus int

const (
	doctorPass doctorStatus = iota
	doctorWarn
	doctorFail
)

// doctorResult holds the outcome of one check.
type doctorResult struct {
	name   string
	status doctorStatus
	detail string // one-line detail shown after the check name
	hint   string // actionable fix hint — printed only on warn/fail
}

// cmdDoctor validates the full Legion stack and prints a flutter-doctor-style
// report with colored pass/fail/warn indicators per check plus a summary line.
//
//	lg doctor
func cmdDoctor() {
	// ANSI colour helpers (reset automatically after each use).
	green := func(s string) string { return "\033[32m" + s + "\033[0m" }
	yellow := func(s string) string { return "\033[33m" + s + "\033[0m" }
	red := func(s string) string { return "\033[31m" + s + "\033[0m" }

	icon := func(s doctorStatus) string {
		switch s {
		case doctorPass:
			return green("✓")
		case doctorWarn:
			return yellow("⚠")
		default:
			return red("✗")
		}
	}

	var results []doctorResult

	// ── 1. Docker daemon ──────────────────────────────────────────────────────
	results = append(results, checkDockerDaemon())

	// ── 2. GH_TOKEN / GITHUB_TOKEN scopes ────────────────────────────────────
	results = append(results, checkGitHubToken())

	// ── 3. Dolt reachable (127.0.0.1:3306) ───────────────────────────────────
	results = append(results, checkDolt())

	// ── 4. Archon container running ───────────────────────────────────────────
	results = append(results, checkArchon())

	// ── 5. Vessel image present and not stale ────────────────────────────────
	results = append(results, checkVesselImage())

	// ── 6. bd CLI installed and configured ───────────────────────────────────
	results = append(results, checkBd())

	// ── 7. git identity set ───────────────────────────────────────────────────
	results = append(results, checkGitIdentity())

	// ── Print report ──────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Legion Doctor")
	fmt.Println(strings.Repeat("─", 50))

	pass, warn, fail := 0, 0, 0
	for _, r := range results {
		switch r.status {
		case doctorPass:
			pass++
		case doctorWarn:
			warn++
		case doctorFail:
			fail++
		}

		line := fmt.Sprintf("  %s  %s", icon(r.status), r.name)
		if r.detail != "" {
			line += "  — " + r.detail
		}
		fmt.Println(line)

		if r.hint != "" && r.status != doctorPass {
			for _, h := range strings.Split(r.hint, "\n") {
				fmt.Printf("       %s\n", h)
			}
		}
	}

	fmt.Println(strings.Repeat("─", 50))
	summary := fmt.Sprintf("  %d passed", pass)
	if warn > 0 {
		summary += fmt.Sprintf(", %d warning(s)", warn)
	}
	if fail > 0 {
		summary += fmt.Sprintf(", %d failed", fail)
	}

	switch {
	case fail > 0:
		fmt.Println(red(summary))
	case warn > 0:
		fmt.Println(yellow(summary))
	default:
		fmt.Println(green(summary))
	}
	fmt.Println()

	if fail > 0 {
		os.Exit(1)
	}
}

// checkDockerDaemon verifies the Docker daemon is reachable.
func checkDockerDaemon() doctorResult {
	r := doctorResult{name: "Docker daemon"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.Output()
	if err != nil {
		r.status = doctorFail
		r.detail = "not reachable"
		r.hint = "Start Docker Desktop or run: sudo systemctl start docker"
		return r
	}
	r.status = doctorPass
	r.detail = "version " + strings.TrimSpace(string(out))
	return r
}

// checkGitHubToken verifies GH_TOKEN / GITHUB_TOKEN is set and has the
// required scopes: repo, workflow, and write:discussion (covers pull_requests:write).
func checkGitHubToken() doctorResult {
	r := doctorResult{name: "GitHub token (GH_TOKEN / GITHUB_TOKEN)"}

	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		r.status = doctorFail
		r.detail = "not set"
		r.hint = "Set GH_TOKEN or GITHUB_TOKEN to a personal access token with repo, workflow, and pull_requests:write scopes.\n" +
			"Create one at: https://github.com/settings/tokens"
		return r
	}

	// Call GitHub API to validate and inspect scopes.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		r.status = doctorWarn
		r.detail = "token set but GitHub API unreachable"
		r.hint = "Check your network connection."
		return r
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		r.status = doctorFail
		r.detail = "token is invalid or expired"
		r.hint = "Regenerate your GitHub token at: https://github.com/settings/tokens"
		return r
	}

	scopes := resp.Header.Get("X-OAuth-Scopes")
	required := []string{"repo", "workflow"}
	var missing []string
	for _, s := range required {
		if !strings.Contains(scopes, s) {
			missing = append(missing, s)
		}
	}
	// pull_requests:write is covered by the "repo" scope on classic tokens, but
	// also accept explicit "pull_requests:write" for fine-grained tokens.
	hasPR := strings.Contains(scopes, "repo") || strings.Contains(scopes, "pull_requests:write")
	if !hasPR {
		missing = append(missing, "pull_requests:write")
	}

	if len(missing) > 0 {
		r.status = doctorFail
		r.detail = fmt.Sprintf("missing scopes: %s (have: %s)", strings.Join(missing, ", "), scopes)
		r.hint = "Regenerate your token with the required scopes: repo, workflow, pull_requests:write\n" +
			"https://github.com/settings/tokens"
		return r
	}

	r.status = doctorPass
	r.detail = "scopes OK"
	return r
}

// checkDolt verifies the Dolt SQL server is reachable at 127.0.0.1:3306.
func checkDolt() doctorResult {
	r := doctorResult{name: "Dolt SQL server (127.0.0.1:3306)"}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:3306", 3*time.Second)
	if err != nil {
		r.status = doctorFail
		r.detail = "not reachable"
		r.hint = "Start the Legion stack: docker compose up -d dolt\n" +
			"Or check that the dolt container is healthy: docker compose ps dolt"
		return r
	}
	_ = conn.Close()
	r.status = doctorPass
	r.detail = "reachable"
	return r
}

// checkArchon verifies the archon container is running.
func checkArchon() doctorResult {
	r := doctorResult{name: "Archon container"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx,
		"docker", "ps", "--filter", "name=archon", "--format", "{{.Names}}").Output()
	if err != nil {
		r.status = doctorFail
		r.detail = "docker ps failed"
		r.hint = "Ensure Docker is running and you have permission to access the socket."
		return r
	}
	names := strings.TrimSpace(string(out))
	if names == "" {
		r.status = doctorFail
		r.detail = "not running"
		r.hint = "Start the Legion stack: docker compose up -d archon\n" +
			"Or check logs: docker compose logs archon"
		return r
	}
	r.status = doctorPass
	r.detail = names
	return r
}

// checkVesselImage verifies the vessel image is present and warns if stale
// (older than 30 days).
func checkVesselImage() doctorResult {
	image := os.Getenv("VESSEL_IMAGE")
	if image == "" {
		image = "legion/vessel-copilot:latest"
	}
	r := doctorResult{name: fmt.Sprintf("Vessel image (%s)", image)}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// docker image inspect returns a JSON array; we read the CreatedAt field.
	out, err := exec.CommandContext(ctx,
		"docker", "image", "inspect", image, "--format", "{{.Created}}").Output()
	if err != nil {
		r.status = doctorFail
		r.detail = "image not found"
		r.hint = fmt.Sprintf("Build the vessel image: docker compose build\n"+
			"Or pull it if it is hosted: docker pull %s", image)
		return r
	}

	created := strings.TrimSpace(string(out))
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		// Fallback: present but can't parse date.
		r.status = doctorPass
		r.detail = "present"
		return r
	}

	age := time.Since(t)
	if age > 30*24*time.Hour {
		r.status = doctorWarn
		r.detail = fmt.Sprintf("present but %.0f days old — consider rebuilding", age.Hours()/24)
		r.hint = "Rebuild: docker compose build"
		return r
	}

	r.status = doctorPass
	r.detail = fmt.Sprintf("present (built %.0f days ago)", age.Hours()/24)
	return r
}

// checkBd verifies the bd CLI is installed and can talk to the database.
func checkBd() doctorResult {
	r := doctorResult{name: "bd CLI"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "bd", "--version").Output()
	if err != nil {
		r.status = doctorFail
		r.detail = "not found"
		r.hint = "Install bd: see https://github.com/EmmittJ/beads for installation instructions.\n" +
			"Ensure it is in your PATH."
		return r
	}
	ver := strings.TrimSpace(string(out))

	// Quick sanity check: can we list issues?
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if _, dbErr := exec.CommandContext(ctx2, "bd", "status").Output(); dbErr != nil {
		r.status = doctorWarn
		r.detail = fmt.Sprintf("%s — installed but database unreachable", ver)
		r.hint = "Run: bd init\nOr ensure the Dolt server is running (see Dolt check above)."
		return r
	}

	r.status = doctorPass
	r.detail = ver
	return r
}

// checkGitIdentity verifies that git user.name and user.email are configured.
func checkGitIdentity() doctorResult {
	r := doctorResult{name: "git identity"}

	nameOut, nameErr := exec.Command("git", "config", "user.name").Output()
	emailOut, emailErr := exec.Command("git", "config", "user.email").Output()

	name := strings.TrimSpace(string(nameOut))
	email := strings.TrimSpace(string(emailOut))

	switch {
	case (nameErr != nil || name == "") && (emailErr != nil || email == ""):
		r.status = doctorFail
		r.detail = "user.name and user.email not set"
		r.hint = `Set your git identity:
  git config --global user.name "Your Name"
  git config --global user.email "you@example.com"`
	case nameErr != nil || name == "":
		r.status = doctorFail
		r.detail = "user.name not set"
		r.hint = `git config --global user.name "Your Name"`
	case emailErr != nil || email == "":
		r.status = doctorFail
		r.detail = "user.email not set"
		r.hint = `git config --global user.email "you@example.com"`
	default:
		r.status = doctorPass
		r.detail = fmt.Sprintf("%s <%s>", name, email)
	}
	return r
}

// gitRoot returns the absolute path to the repository root by running git rev-parse --show-toplevel.
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
