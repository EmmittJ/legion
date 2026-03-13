package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
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
	fmt.Fprintln(os.Stderr, "  lg invoke \"<title>\"   — create a new task issue")
	fmt.Fprintln(os.Stderr, "  lg status              — list open and in-progress issues")
	fmt.Fprintln(os.Stderr, "  lg log <issue-id>      — show traces for an issue")
	fmt.Fprintln(os.Stderr, "  lg watch [--interval=N] — live-refreshing status dashboard (default: 3s)")
}

// cmdInvoke creates a new Beads task issue.
//
//	lg invoke "Fix the login bug"
func cmdInvoke() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "lg invoke: title required")
		fmt.Fprintln(os.Stderr, "  usage: lg invoke \"<title>\"")
		os.Exit(1)
	}
	title := os.Args[2]

	out, err := bdOutput("create", title, "--type=task", "--json")
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
		ID         string `json:"id"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		AssignedTo string `json:"assigned_to"`
	}

	var allIssues []issueCore

	for _, status := range []string{"open", "in_progress"} {
		out, err := bdOutput("list", "--status="+status, "--json")
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
		assignedTo := iss.AssignedTo
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

// cmdLog shows traces/notes for a single issue.
//
//	lg log <issue-id>
func cmdLog() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "lg log: issue-id required")
		fmt.Fprintln(os.Stderr, "  usage: lg log <issue-id>")
		os.Exit(1)
	}
	issueID := os.Args[2]

	out, err := bdOutput("show", issueID, "--json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "lg log: %v\n", err)
		os.Exit(1)
	}

	type noteEntry struct {
		Timestamp string `json:"timestamp"`
		Content   string `json:"content"`
	}
	type issueDetail struct {
		ID          string      `json:"id"`
		Title       string      `json:"title"`
		Status      string      `json:"status"`
		Description string      `json:"description"`
		AssignedTo  string      `json:"assigned_to"`
		Notes       []noteEntry `json:"notes"`
		Traces      []noteEntry `json:"traces"`
	}

	// bd show returns a single-element flat array: [{"id":"...","title":"...",...}]
	var items []issueDetail
	if err := json.Unmarshal(out, &items); err != nil || len(items) == 0 {
		// Fallback: print raw JSON if parsing fails or array is empty.
		fmt.Println(string(out))
		return
	}

	detail := items[0]

	fmt.Printf("Issue: %s — %s [%s]\n", detail.ID, detail.Title, detail.Status)
	if detail.Description != "" {
		fmt.Printf("Description: %s\n", detail.Description)
	}
	if detail.AssignedTo != "" {
		fmt.Printf("Assigned to: %s\n", detail.AssignedTo)
	}

	entries := detail.Notes
	if len(entries) == 0 && len(detail.Traces) > 0 {
		// Use traces field if notes is empty.
		entries = append(entries, detail.Traces...)
	}

	if len(entries) == 0 {
		fmt.Println("\n(no traces)")
		return
	}

	fmt.Printf("\nTraces (%d):\n", len(entries))
	for i, entry := range entries {
		ts := entry.Timestamp
		if ts == "" {
			ts = "—"
		}
		fmt.Printf("  [%d] %s  %s\n", i+1, ts, entry.Content)
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
		ID         string `json:"id"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		AssignedTo string `json:"assigned_to"`
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
		out, err := bdOutput("list", "--status="+status, "--json")
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
				assignedTo := iss.AssignedTo
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

// bdOutput runs a bd subcommand and returns stdout.
func bdOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("bd", args...)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("bd %s: %w\nstderr: %s", strings.Join(args, " "), err, stderr.String())
		}
		return nil, fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
