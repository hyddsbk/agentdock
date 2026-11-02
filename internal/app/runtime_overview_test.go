package app

import (
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestRuntimeOverviewMatchesNexusContract(t *testing.T) {
	cfg := config.Config{
		AgentDockDefaultDir: t.TempDir(),
		AgentDockHome:       filepath.Join(t.TempDir(), ".agentdock"),
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	result, err := rt.RuntimeOverview()
	if err != nil {
		t.Fatal(err)
	}
	tasks, ok := result["tasks"].(Result)
	if !ok {
		t.Fatalf("tasks = %#v", result["tasks"])
	}
	for _, key := range []string{"active", "completed", "blocked", "active_recent_24h"} {
		if _, exists := tasks[key]; !exists {
			t.Fatalf("tasks missing %q: %#v", key, tasks)
		}
	}
	skills, ok := result["skills"].(Result)
	if !ok {
		t.Fatalf("skills = %#v", result["skills"])
	}
	if _, exists := skills["count"]; !exists {
		t.Fatalf("skills missing count: %#v", skills)
	}
	plugins, ok := result["plugins"].(Result)
	if !ok {
		t.Fatalf("plugins = %#v", result["plugins"])
	}
	if plugins["count"] != 0 || plugins["available"] != false {
		t.Fatalf("plugins = %#v", plugins)
	}
	mcp, ok := result["mcp"].(Result)
	if !ok {
		t.Fatalf("mcp = %#v", result["mcp"])
	}
	if _, exists := mcp["count"]; !exists {
		t.Fatalf("mcp missing count: %#v", mcp)
	}
}
