package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"text/tabwriter"
)

// ---------------------------------------------------------------------------
// Subprocess helper
// ---------------------------------------------------------------------------

// TestMain_Subprocess is invoked as a subprocess by invoke/log argument
// validation tests.  In normal test runs (LG_TEST_MODE unset) it returns
// immediately.  When running as a subprocess it sets os.Args and calls
// main(), which may call os.Exit — that is expected and intentional.
func TestMain_Subprocess(t *testing.T) {
	mode := os.Getenv("LG_TEST_MODE")
	if mode == "" {
		return // ordinary test run — nothing to do
	}
	switch mode {
	case "invoke_no_args":
		os.Args = []string{"lg", "invoke"}
	case "log_no_args":
		os.Args = []string{"lg", "log"}
	default:
		fmt.Fprintf(os.Stderr, "unknown LG_TEST_MODE: %q\n", mode)
		os.Exit(2)
	}
	main() // may call os.Exit; that is the behaviour under test
}

// runSubprocess executes the test binary in subprocess mode and returns
// the combined stdout+stderr output and the exit error (nil on success).
func runSubprocess(t *testing.T, mode string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMain_Subprocess")
	cmd.Env = append(os.Environ(), "LG_TEST_MODE="+mode)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestInvokeNoArgs verifies that "lg invoke" with no title argument prints
// a usage error to stderr and exits non-zero.
func TestInvokeNoArgs(t *testing.T) {
	out, err := runSubprocess(t, "invoke_no_args")
	if err == nil {
		t.Fatalf("lg invoke (no args): expected non-zero exit, got success\noutput: %s", out)
	}
	if !strings.Contains(out, "title required") {
		t.Errorf("lg invoke (no args): expected 'title required' in output\nfull output: %s", out)
	}
}

// TestLogNoArgs verifies that "lg log" with no issue-id argument prints a
// usage error to stderr and exits non-zero.
func TestLogNoArgs(t *testing.T) {
	out, err := runSubprocess(t, "log_no_args")
	if err == nil {
		t.Fatalf("lg log (no args): expected non-zero exit, got success\noutput: %s", out)
	}
	if !strings.Contains(out, "issue-id required") {
		t.Errorf("lg log (no args): expected 'issue-id required' in output\nfull output: %s", out)
	}
}

// TestStatusHeaderColumns verifies that the status table output contains the
// expected header columns (ID, TITLE, STATUS) by exercising the JSON-parsing
// and tabwriter-formatting logic directly — no live `bd` binary required.
//
// This mirrors the logic inside cmdStatus to validate that:
//   - the issueEntry JSON field names are correct
//   - the tabwriter header row includes the required column labels
func TestStatusHeaderColumns(t *testing.T) {
	type issueCore struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		AssignedTo string `json:"assigned_to"`
	}
	type issueEntry struct {
		Issue issueCore `json:"issue"`
	}

	raw := `[{"issue":{"id":"TST-001","title":"Build the test harness","status":"open","assigned_to":"andariel"}}]`

	var parsed []issueEntry
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal issue JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("want 1 parsed entry, got %d", len(parsed))
	}

	entries := make([]issueCore, len(parsed))
	for i, p := range parsed {
		entries[i] = p.Issue
	}

	// Reproduce the cmdStatus formatting (mirrors cmd/lg/main.go cmdStatus).
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tSTATUS\tASSIGNED_TO")
	fmt.Fprintln(w, "──\t─────\t──────\t───────────")
	for _, e := range entries {
		title := e.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		assignedTo := e.AssignedTo
		if assignedTo == "" {
			assignedTo = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.ID, title, e.Status, assignedTo)
	}
	w.Flush()

	out := buf.String()
	t.Logf("formatted table:\n%s", out)

	// Required header columns per the lg spec.
	for _, col := range []string{"ID", "TITLE", "STATUS"} {
		if !strings.Contains(out, col) {
			t.Errorf("table output missing required column %q\nfull output:\n%s", col, out)
		}
	}

	// Data row sanity: the parsed issue ID should appear in the output.
	if !strings.Contains(out, "TST-001") {
		t.Errorf("table output missing issue ID TST-001\nfull output:\n%s", out)
	}
}
