package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/EmmittJ/legion/internal/acp"
)

func main() {
	issueID := requireEnv("ISSUE_ID")
	repoURL := requireEnv("REPO_URL")
	githubToken := requireEnv("GITHUB_TOKEN")
	_ = requireEnv("DOLT_DSN") // validated at startup; used implicitly by bd CLI

	// Step 2: Read issue from Beads.
	issue, err := bdShow(issueID)
	if err != nil {
		log.Fatalf("vessel-driver: bd show %s: %v", issueID, err)
	}

	// Step 3: Clone the repo.
	if err := runCmd("", "git", "clone", repoURL, "/workspace"); err != nil {
		markFailed(issueID, "git clone failed")
		log.Fatalf("vessel-driver: git clone: %v", err)
	}

	// Step 4: Checkout branch.
	branch := "legion/" + issueID
	if err := runCmd("/workspace", "git", "checkout", "-b", branch); err != nil {
		markFailed(issueID, "checkout failed")
		log.Fatalf("vessel-driver: git checkout: %v", err)
	}

	ctx := context.Background()

	// Step 5: Start ACP server.
	model := os.Getenv("VESSEL_MODEL")
	client, err := acp.New(ctx, model)
	if err != nil {
		markFailed(issueID, "ACP start failed")
		log.Fatalf("vessel-driver: acp.New: %v", err)
	}
	defer client.Close()

	// Step 6: Initialize.
	protocolVersion, err := client.Initialize()
	if err != nil {
		markFailed(issueID, "ACP error")
		log.Fatalf("vessel-driver: acp.Initialize: %v", err)
	}
	log.Printf("vessel-driver: ACP handshake OK — protocol version %d", protocolVersion)

	// Step 7: New session.
	sessionID, err := client.NewSession("/workspace")
	if err != nil {
		markFailed(issueID, "ACP error")
		log.Fatalf("vessel-driver: acp.NewSession: %v", err)
	}
	log.Printf("vessel-driver: session %s ready", sessionID)

	// Step 8: Prompt with issue content.
	promptContent := issue.Title + "\n\n" + issue.Description

	onUpdate := func(update map[string]any) {
		raw, err := json.Marshal(update)
		if err != nil {
			return
		}
		note := string(raw)
		// Best-effort — don't fail the whole operation if a trace write fails.
		_ = runCmd("", "bd", "update", issueID, "--append-notes", note)
	}

	// Determine prompt timeout — default 45 min, overrideable via VESSEL_TIMEOUT (seconds).
	timeoutSecs := 2700
	if v := os.Getenv("VESSEL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			timeoutSecs = n
		}
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()
	stopReason, err := client.Prompt(promptCtx, sessionID, promptContent, onUpdate)

	// Step 9/10: Handle completion.
	if err != nil || stopReason == "error" {
		if err != nil {
			log.Printf("vessel-driver: prompt error: %v", err)
		} else {
			log.Printf("vessel-driver: prompt stopped with error reason")
		}
		markFailed(issueID, "ACP error")
		os.Exit(1)
	}

	log.Printf("vessel-driver: prompt complete — stop reason: %s", stopReason)

	// Step 9a: git add -A.
	if err := runCmd("/workspace", "git", "add", "-A"); err != nil {
		markFailed(issueID, "git add failed")
		log.Fatalf("vessel-driver: git add: %v", err)
	}

	// Step 9b: git commit.
	commitMsg := fmt.Sprintf("feat(%s): %s", issueID, issue.Title)
	if err := runCmd("/workspace",
		"git",
		"-c", "user.email=vessel@legion",
		"-c", "user.name=Vessel",
		"commit", "-m", commitMsg,
	); err != nil {
		markFailed(issueID, "git commit failed")
		log.Fatalf("vessel-driver: git commit: %v", err)
	}

	// Step 9c: git push.
	// Inject token into the origin remote URL so the push authenticates without
	// exposing the credential as a command-line argument (visible in `ps aux`).
	pushURL, err := buildPushURL(repoURL, githubToken)
	if err != nil {
		markFailed(issueID, "git push failed")
		log.Fatalf("vessel-driver: build push URL: %v", err)
	}
	if err := runCmd("/workspace", "git", "remote", "set-url", "origin", pushURL); err != nil {
		markFailed(issueID, "git push failed")
		log.Fatalf("vessel-driver: git remote set-url: %v", err)
	}
	if err := runCmd("/workspace", "git", "push", "origin", branch); err != nil {
		markFailed(issueID, "git push failed")
		log.Fatalf("vessel-driver: git push: %v", err)
	}

	// Step 9d: close the issue.
	if err := runCmd("", "bd", "close", issueID, "--reason", "completed"); err != nil {
		log.Printf("vessel-driver: warning: bd close failed: %v", err)
	}

	log.Printf("vessel-driver: issue %s closed — branch %s pushed", issueID, branch)
	os.Exit(0)
}

// requireEnv returns the value of an env var or fatals.
func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("vessel-driver: required env var %s is not set", name)
	}
	return v
}

// issueCore holds the fields of a Beads issue nested inside the "issue" envelope.
type issueCore struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// issueDetails is the envelope returned by `bd show <id> --json`.
type issueDetails struct {
	Issue issueCore `json:"issue"`
}

// bdShow calls `bd show <id> --json` and parses the result.
func bdShow(id string) (*issueCore, error) {
	out, err := execOutput("", "bd", "show", id, "--json")
	if err != nil {
		return nil, err
	}
	var env issueDetails
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("bd show: parse JSON: %w", err)
	}
	return &env.Issue, nil
}

// markFailed marks an issue as blocked with a reason. Errors are logged but not fatal.
func markFailed(issueID, reason string) {
	if err := runCmd("", "bd", "update", issueID, "--status=blocked", "--append-notes="+reason); err != nil {
		log.Printf("vessel-driver: warning: could not mark issue %s blocked: %v", issueID, err)
	}
}

// runCmd runs a command in dir, logging stderr on failure.
func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%s %v: %w\nstderr: %s", name, args, err, stderr.String())
		}
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

// execOutput runs a command and captures stdout.
func execOutput(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s %v: %w\nstderr: %s", name, args, err, stderr.String())
		}
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return out, nil
}

// buildPushURL injects the GitHub token into the repo URL.
// Handles both https://github.com/owner/repo and git@github.com:owner/repo formats.
func buildPushURL(repoURL, token string) (string, error) {
	// Convert SSH format to HTTPS.
	httpsURL := repoURL
	if strings.HasPrefix(repoURL, "git@github.com:") {
		path := strings.TrimPrefix(repoURL, "git@github.com:")
		httpsURL = "https://github.com/" + path
	}

	// Ensure .git suffix for consistency.
	if !strings.HasSuffix(httpsURL, ".git") {
		httpsURL += ".git"
	}

	u, err := url.Parse(httpsURL)
	if err != nil {
		return "", fmt.Errorf("parse repo URL %q: %w", httpsURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("repo URL %q has no host", httpsURL)
	}

	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}
