package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/EmmittJ/legion/internal/config"
)

func TestResolveACPCommand(t *testing.T) {
	t.Run("copilot with model and agent set", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
			Model:     "gpt-5-mini",
			AgentFile: "baal",
		}
		got, err := resolveACPCommand(spec, "", "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{
			"copilot", "--acp", "--stdio",
			"--model", "gpt-5-mini",
			"--agent", "/workspace/.github/agents/baal.agent.md",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("copilot with no model and no agent", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
		}
		got, err := resolveACPCommand(spec, "", "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"copilot", "--acp", "--stdio"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("LEGION_MODEL override wins over spec.Model", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
			Model:     "gpt-4",
		}
		got, err := resolveACPCommand(spec, "gpt-5-mini", "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"copilot", "--acp", "--stdio", "--model", "gpt-5-mini"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("LEGION_MODEL env var overrides spec.Model end-to-end", func(t *testing.T) {
		t.Setenv("LEGION_MODEL", "claude-sonnet")
		legionModel := os.Getenv("LEGION_MODEL")
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
			Model:     "gpt-4",
		}
		got, err := resolveACPCommand(spec, legionModel, "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"copilot", "--acp", "--stdio", "--model", "claude-sonnet"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("backend=raw with AgentFile set", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "raw",
			AgentFile: "/usr/local/bin/my-agent --flag",
		}
		got, err := resolveACPCommand(spec, "", "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"/usr/local/bin/my-agent", "--flag"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("backend=raw with empty AgentFile returns error", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "raw",
		}
		_, err := resolveACPCommand(spec, "", "/workspace")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("unknown backend returns error", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "grpc",
		}
		_, err := resolveACPCommand(spec, "", "/workspace")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("copilot agent_file with slash not expanded", func(t *testing.T) {
		spec := config.ACPSpec{
			Transport: "stdio",
			Backend:   "copilot",
			AgentFile: "/absolute/path/to/agent.agent.md",
		}
		got, err := resolveACPCommand(spec, "", "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{
			"copilot", "--acp", "--stdio",
			"--agent", "/absolute/path/to/agent.agent.md",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestExtractAndWriteDecision(t *testing.T) {
	ctx := context.Background()

	readDecision := func(t *testing.T, dir string) map[string]string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, ".legion", "decision.json"))
		if err != nil {
			t.Fatalf("read decision.json: %v", err)
		}
		var d map[string]string
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatalf("parse decision.json: %v", err)
		}
		return d
	}

	t.Run("APPROVE at end of output", func(t *testing.T) {
		dir := t.TempDir()
		output := "I reviewed the diff carefully.\n\nAll AC are met.\n\nVERDICT: APPROVE\n"
		if err := extractAndWriteDecision(ctx, output, dir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := readDecision(t, dir)
		if d["decision"] != "APPROVE" {
			t.Errorf("decision = %q, want APPROVE", d["decision"])
		}
		if d["reason"] != "Approved by inquisitor" {
			t.Errorf("reason = %q, want 'Approved by inquisitor'", d["reason"])
		}
	})

	t.Run("REJECT with reason lines", func(t *testing.T) {
		dir := t.TempDir()
		output := "AC item 2 is not satisfied.\n\nVERDICT: REJECT\nReason: Add validation in vessel.go line 42.\nAlso fix the test."
		if err := extractAndWriteDecision(ctx, output, dir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := readDecision(t, dir)
		if d["decision"] != "REJECT" {
			t.Errorf("decision = %q, want REJECT", d["decision"])
		}
		want := "Reason: Add validation in vessel.go line 42.\nAlso fix the test."
		if d["reason"] != want {
			t.Errorf("reason = %q, want %q", d["reason"], want)
		}
	})

	t.Run("REJECT with no reason lines uses fallback", func(t *testing.T) {
		dir := t.TempDir()
		output := "VERDICT: REJECT\n"
		if err := extractAndWriteDecision(ctx, output, dir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := readDecision(t, dir)
		if d["decision"] != "REJECT" {
			t.Errorf("decision = %q, want REJECT", d["decision"])
		}
		if d["reason"] == "" {
			t.Error("reason should not be empty when no reason lines provided")
		}
	})

	t.Run("VERDICT with leading/trailing whitespace is still matched", func(t *testing.T) {
		dir := t.TempDir()
		output := "  VERDICT: APPROVE  \n"
		if err := extractAndWriteDecision(ctx, output, dir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := readDecision(t, dir)
		if d["decision"] != "APPROVE" {
			t.Errorf("decision = %q, want APPROVE", d["decision"])
		}
	})

	t.Run("no VERDICT line returns error", func(t *testing.T) {
		dir := t.TempDir()
		output := "I looked at the diff. Everything seems fine."
		err := extractAndWriteDecision(ctx, output, dir)
		if err == nil {
			t.Fatal("expected error for missing VERDICT, got nil")
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".legion", "decision.json")); statErr == nil {
			t.Error("decision.json should not be written on error")
		}
	})

	t.Run("invalid VERDICT value returns error", func(t *testing.T) {
		dir := t.TempDir()
		output := "VERDICT: MAYBE\n"
		err := extractAndWriteDecision(ctx, output, dir)
		if err == nil {
			t.Fatal("expected error for invalid VERDICT value, got nil")
		}
	})

	t.Run("VERDICT mid-output is found correctly", func(t *testing.T) {
		dir := t.TempDir()
		output := "Some preamble.\nVERDICT: APPROVE\nSome trailing text."
		if err := extractAndWriteDecision(ctx, output, dir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := readDecision(t, dir)
		if d["decision"] != "APPROVE" {
			t.Errorf("decision = %q, want APPROVE", d["decision"])
		}
	})
}
