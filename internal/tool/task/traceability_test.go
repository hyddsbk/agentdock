package task

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskManageTraceabilityLifecycle(t *testing.T) {
	rt, _ := newTaskTestService(t)
	created, err := rt.manageTest(context.Background(), map[string]any{
		"action":                "create",
		"title":                 "Traceable change",
		"goal":                  "implement settled spec",
		"completion_conditions": []string{"requirements are evidenced"},
		"requirement_refs":      []string{"spec://feature#R1"},
		"steps": []map[string]any{{
			"id": "build", "title": "Build",
			"completion_conditions": []string{"R1 works."},
			"requirement_refs":      []string{"spec://feature#R1"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	summary := created["task_summary"].(map[string]any)
	if summary["requirement_ref_count"] != 1 {
		t.Fatalf("create summary lost requirement refs: %#v", summary)
	}

	if _, err := rt.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "build", "status": "completed", "summary": "claimed complete",
	}); err == nil {
		t.Fatal("traceable task_manage checkpoint accepted completion without execution_evidence")
	}

	progress, err := rt.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "build", "status": "completed", "summary": "implemented",
		"execution_evidence": map[string]any{"evidence_refs": []string{"commit://agentdock/abc123", "test://task/TestR1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	step := progress["task_summary"].(map[string]any)["steps"].([]map[string]any)[0]
	if step["execution_evidence"] == nil {
		t.Fatalf("execution evidence missing from compact result: %#v", step)
	}

	reviewed, err := rt.manageTest(context.Background(), map[string]any{
		"action": "final_review", "task_id": taskID, "status": "pass", "summary": "covered",
		"verified":           []string{"R1 covered"},
		"intent_spec":        map[string]any{"status": "pass", "evidence": []string{"traceability coverage complete"}},
		"engineering_policy": map[string]any{"status": "pass", "evidence": []string{"tests pass"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	final := reviewed["task_summary"].(map[string]any)["final_review"].(map[string]any)
	coverage, ok := final["traceability"].(*taskstate.TraceabilityCoverage)
	if !ok || coverage.RequiredCount != 1 || coverage.CoveredCount != 1 {
		t.Fatalf("final review coverage = %#v", final["traceability"])
	}
}

func TestTaskManageSchemaPublishesTraceabilityFields(t *testing.T) {
	schema := ManageInputSchema(config.Config{})
	props := schema["properties"].(map[string]any)
	for _, key := range []string{"requirement_refs", "execution_evidence"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("task_manage schema missing %s", key)
		}
	}
	steps := props["steps"].(map[string]any)
	items := steps["items"].(map[string]any)
	stepProps := items["properties"].(map[string]any)
	if _, ok := stepProps["requirement_refs"]; !ok {
		t.Fatal("task_manage step schema missing requirement_refs")
	}
}
