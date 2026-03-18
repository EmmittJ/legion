package main

import (
	"os"
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
