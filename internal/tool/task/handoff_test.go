package task

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestTaskManageSchemaExposesHandoffResumeContract(t *testing.T) {
	schema := ManageInputSchema(config.Config{})
	props := schema["properties"].(map[string]any)
	for _, field := range []string{"next_step_id", "context_refs"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("input schema missing %s", field)
		}
	}
	action := props["action"].(map[string]any)
	found := false
	for _, value := range action["enum"].([]string) {
		if value == "handoff" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("handoff action missing: %#v", action["enum"])
	}
	output := ManageOutputSchema(config.Config{})
	outputProps := output["properties"].(map[string]any)
	if _, ok := outputProps["resume_context"]; !ok {
		t.Fatal("output schema missing resume_context")
	}
}

func TestTaskManageHandoffReturnsDerivedResumeContextAndGetPreservesIt(t *testing.T) {
	rt, _ := newTaskTestService(t)
	created, err := rt.manageTest(context.Background(), map[string]any{
		"action":                "create",
		"title":                 "Handoff",
		"goal":                  "resume in a fresh session",
		"completion_conditions": []string{"build complete"},
		"steps": []map[string]any{
			{
				"id": "inspect", "title": "Inspect",
				"completion_conditions": []string{"Inspection complete."},
				"context_refs":          []string{"artifact://inspect/latest"},
			},
			{
				"id": "build", "title": "Build",
				"depends_on":            []string{"inspect"},
				"completion_conditions": []string{"Build verified."},
				"context_refs":          []string{"spec://feature/current", "artifact://inspect/latest"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	if _, err := rt.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "inspect", "status": "completed", "summary": "inspection complete",
	}); err != nil {
		t.Fatal(err)
	}

	handoff, err := rt.manageTest(context.Background(), map[string]any{
		"action":       "handoff",
		"task_id":      taskID,
		"next_step_id": "build",
		"summary":      "Inspection complete; resume at build from the pinned spec.",
		"context_refs": []string{"artifact://inspect/latest", "commit://abc123"},
		"risks":        []string{"Production activation still needs rollback verification."},
	})
	if err != nil {
		t.Fatal(err)
	}
	resume, ok := handoff["resume_context"].(map[string]any)
	if !ok {
		t.Fatalf("resume_context = %#v", handoff["resume_context"])
	}
	next, ok := resume["next_step"].(map[string]any)
	if !ok || next["id"] != "build" {
		t.Fatalf("next_step = %#v", resume["next_step"])
	}
	frontier, ok := resume["ready_frontier"].([]map[string]any)
	if !ok || len(frontier) != 1 || frontier[0]["id"] != "build" {
		t.Fatalf("ready_frontier = %#v", resume["ready_frontier"])
	}
	refs, ok := resume["context_refs"].([]string)
	if !ok {
		t.Fatalf("context_refs type = %T", resume["context_refs"])
	}
	wantRefs := []string{"artifact://inspect/latest", "commit://abc123", "spec://feature/current"}
	if len(refs) != len(wantRefs) {
		t.Fatalf("context_refs = %#v, want %#v", refs, wantRefs)
	}
	for i := range wantRefs {
		if refs[i] != wantRefs[i] {
			t.Fatalf("context_refs = %#v, want %#v", refs, wantRefs)
		}
	}
	if risks, ok := resume["risks"].([]string); !ok || len(risks) != 1 {
		t.Fatalf("risks = %#v", resume["risks"])
	}

	loaded, err := rt.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded["resume_context"]; !ok {
		t.Fatalf("get omitted resume_context: %#v", loaded)
	}

	if _, err := rt.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "build", "status": "in_progress", "summary": "build started",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = rt.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded["resume_context"]; ok {
		t.Fatalf("stale resume_context survived mutation: %#v", loaded["resume_context"])
	}
}

func TestTaskManageHandoffRejectsBlockedNextStepAndMisplacedPointers(t *testing.T) {
	rt, _ := newTaskTestService(t)
	created, err := rt.manageTest(context.Background(), map[string]any{
		"action":                "create",
		"title":                 "Handoff validation",
		"goal":                  "reject stale routing",
		"completion_conditions": []string{"done"},
		"steps": []map[string]any{
			{"id": "inspect", "title": "Inspect", "completion_conditions": []string{"Inspected."}},
			{"id": "build", "title": "Build", "depends_on": []string{"inspect"}, "completion_conditions": []string{"Built."}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	if _, err := rt.manageTest(context.Background(), map[string]any{
		"action": "handoff", "task_id": taskID, "next_step_id": "build", "summary": "too early",
	}); err == nil {
		t.Fatal("handoff accepted dependency-blocked next step")
	}
	if _, err := rt.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "inspect", "status": "in_progress", "summary": "started",
		"context_refs": []string{"artifact://not-valid-here"},
	}); err == nil {
		t.Fatal("checkpoint accepted top-level handoff context_refs")
	}
}
