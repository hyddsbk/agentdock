package task

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskManageEvidenceSemanticsRoundTripAndReviewGate(t *testing.T) {
	service, _ := newTaskTestService(t)
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "P12 semantics", "goal": "preserve bounded evidence",
		"completion_conditions": []string{"bounded claim reviewed"},
		"steps":                 []map[string]any{{"id": "inspect", "title": "Inspect"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)

	progress, err := service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "inspect", "status": "completed", "summary": "bounded scan complete",
		"evidence_records": []map[string]any{{
			"statement":   "No direct edge was observed inside the bounded static scan.",
			"fact_status": "observed", "source_refs": []string{"artifact://scan/p12"},
			"coverage": "partial", "limitations": []string{"Dynamic dispatch was outside the boundary."},
			"partial_result": true, "untrusted_content": true,
		}},
		"unknown_updates": []map[string]any{{
			"action": "open", "question": "Could a dynamic edge still exist?", "blocking": false,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	summary := progress["task_summary"].(map[string]any)
	steps := summary["steps"].([]map[string]any)
	records, ok := steps[0]["evidence_records"].([]taskstate.EvidenceRecord)
	if !ok || len(records) != 1 || records[0].Coverage != taskstate.EvidenceCoveragePartial || !records[0].UntrustedContent {
		t.Fatalf("semantic evidence missing from checkpoint summary: %#v", steps[0]["evidence_records"])
	}
	openUnknowns, ok := summary["open_unknowns"].([]map[string]any)
	if !ok || len(openUnknowns) != 1 {
		t.Fatalf("open unknown summary = %#v", summary["open_unknowns"])
	}

	loaded, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	task := loaded["task"].(taskstate.Task)
	evidenceRef := "evidence://" + task.Steps[0].EvidenceRecords[0].ID
	unknownID := task.Unknowns[0].ID

	claim := "Only the bounded static observation is claimed."
	intentClaim := "intent scope is bounded"
	policyClaim := "unknown is explicitly carried"
	review, err := service.manageTest(context.Background(), map[string]any{
		"action": "final_review", "task_id": taskID, "status": "pass", "summary": "bounded claim accepted",
		"verified": []string{claim},
		"verified_evidence": []map[string]any{{
			"claim": claim, "step_id": "inspect", "evidence_refs": []string{evidenceRef},
		}},
		"acknowledged_unknown_ids": []string{unknownID},
		"intent_spec": map[string]any{
			"status": "pass", "evidence": []string{intentClaim},
			"evidence_bindings": []map[string]any{{"claim": intentClaim, "step_id": "inspect", "evidence_refs": []string{evidenceRef}}},
		},
		"engineering_policy": map[string]any{
			"status": "pass", "evidence": []string{policyClaim},
			"evidence_bindings": []map[string]any{{"claim": policyClaim, "step_id": "inspect", "evidence_refs": []string{evidenceRef}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if review["review_revision"] == "" {
		t.Fatalf("final review omitted revision: %#v", review)
	}
	loaded, err = service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	task = loaded["task"].(taskstate.Task)
	if task.FinalReview == nil || len(task.FinalReview.AcknowledgedUnknownIDs) != 1 || task.FinalReview.AcknowledgedUnknownIDs[0] != unknownID {
		t.Fatalf("acknowledged unknown did not survive task_manage round trip: %#v", task.FinalReview)
	}
}

func TestTaskManageSchemaPublishesEvidenceSemantics(t *testing.T) {
	schema := ManageInputSchema(config.Config{})
	props := schema["properties"].(map[string]any)
	for _, field := range []string{"evidence_records", "unknown_updates", "acknowledged_unknown_ids"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("task_manage schema missing %s", field)
		}
	}
	evidenceItems := props["evidence_records"].(map[string]any)["items"].(map[string]any)
	evidenceProps := evidenceItems["properties"].(map[string]any)
	for _, field := range []string{"statement", "fact_status", "source_refs", "coverage", "limitations", "untrusted_content", "partial_result"} {
		if _, ok := evidenceProps[field]; !ok {
			t.Fatalf("evidence_records schema missing %s", field)
		}
	}
}
