package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
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
	type issueEntry struct {
		Issue issueCore `json:"issue"`
	}

	var allIssues []issueCore

	for _, status := range []string{"open", "in_progress"} {
		out, err := bdOutput("list", "--status="+status, "--json")
		if err != nil {
			fmt.Fprintf(os.Stderr, "lg status: bd list --status=%s: %v\n", status, err)
			continue
		}

		var batch []issueEntry
		if err := json.Unmarshal(out, &batch); err != nil {
			fmt.Fprintf(os.Stderr, "lg status: parse %s issues: %v\n", status, err)
			continue
		}
		for _, e := range batch {
			allIssues = append(allIssues, e.Issue)
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

	var env struct {
		Issue struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			Description string `json:"description"`
			AssignedTo  string `json:"assigned_to"`
			Notes       []struct {
				Timestamp string `json:"timestamp"`
				Content   string `json:"content"`
			} `json:"notes"`
			Traces []struct {
				Timestamp string `json:"timestamp"`
				Content   string `json:"content"`
			} `json:"traces"`
		} `json:"issue"`
	}

	if err := json.Unmarshal(out, &env); err != nil {
		// Fallback: print raw JSON if parsing fails.
		fmt.Println(string(out))
		return
	}

	detail := env.Issue

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
		for _, t := range detail.Traces {
			entries = append(entries, struct {
				Timestamp string `json:"timestamp"`
				Content   string `json:"content"`
			}{Timestamp: t.Timestamp, Content: t.Content})
		}
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
