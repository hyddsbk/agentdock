package task

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestTaskManageImprovementExperimentEndToEnd(t *testing.T) {
	service, _ := newTaskTestService(t)
	ctx := context.Background()

	created, err := service.manageTest(ctx, map[string]any{
		"action":                "create",
		"title":                 "Refine first-party harness",
		"goal":                  "compare first-party harness refinements without creating a second evolution authority",
		"completion_conditions": []string{"composed refinement is selected from probe evidence"},
		"improvement_experiment": map[string]any{
			"target":         "skill-authoring",
			"mode":           "parallel",
			"probe_criteria": []string{"same task succeeds", "no authority collision"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)

	register := func(scope, description, artifact string) (string, string) {
		t.Helper()
		blindSum := sha256.Sum256([]byte(artifact))
		result, err := service.manageTest(ctx, map[string]any{
			"action":  "improvement_update",
			"task_id": taskID,
			"improvement_update": map[string]any{
				"action": "register_candidate",
				"candidate": map[string]any{
					"scope": scope, "description": description, "artifact_ref": artifact,
					"blind_artifact_ref": fmt.Sprintf("artifact://anon-%x", blindSum),
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		candidate := result["improvement_candidate"].(map[string]any)
		return candidate["id"].(string), candidate["blind_alias"].(string)
	}

	baseID, baseAlias := register("base", "unmodified baseline", "artifact://harness/base")
	candidateID, candidateAlias := register("invariant", "move reusable procedure to an on-demand Skill", "artifact://harness/invariant")

	for _, probe := range []map[string]any{
		{"id": baseID, "summary": "baseline works but carries repeated standing context", "ref": "artifact://probe/base"},
		{"id": candidateID, "summary": "candidate works with less standing context", "ref": "artifact://probe/invariant"},
	} {
		if _, err := service.manageTest(ctx, map[string]any{
			"action": "improvement_update", "task_id": taskID,
			"improvement_update": map[string]any{
				"action": "record_probe",
				"probe": map[string]any{
					"candidate_id": probe["id"], "summary": probe["summary"], "evidence_refs": []string{probe["ref"].(string)},
				},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	blind, err := service.manageTest(ctx, map[string]any{"action": "get_improvement_view", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(blind["improvement_view"])
	if err != nil {
		t.Fatal(err)
	}
	blindJSON := string(encoded)
	for _, forbidden := range []string{baseID, candidateID, "unmodified baseline", "move reusable procedure", "artifact://harness/base", "artifact://harness/invariant", "artifact://probe/base", "artifact://probe/invariant", "\"scope\"", "\"evidence_refs\""} {
		if strings.Contains(blindJSON, forbidden) {
			t.Fatalf("blind tool view leaked %q: %s", forbidden, blindJSON)
		}
	}
	if !strings.Contains(blindJSON, baseAlias) || !strings.Contains(blindJSON, candidateAlias) {
		t.Fatalf("blind aliases missing: %s", blindJSON)
	}

	if _, err := service.manageTest(ctx, map[string]any{
		"action": "improvement_update", "task_id": taskID,
		"improvement_update": map[string]any{
			"action": "select",
			"selection": map[string]any{
				"aliases": []string{candidateAlias}, "rationale": "anonymous evidence favors lower standing-context cost",
				"evidence_refs": []string{"artifact://blind-selection"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	composed, err := service.manageTest(ctx, map[string]any{
		"action": "improvement_update", "task_id": taskID,
		"improvement_update": map[string]any{
			"action": "compose",
			"composition": map[string]any{
				"summary":       "adopt the selected on-demand instruction placement",
				"lesson":        "Prefer an on-demand Skill for reusable procedures with recognizable triggers; reserve always-on text for rules that truly apply to every run.",
				"artifact_ref":  "artifact://harness/composed",
				"evidence_refs": []string{"artifact://regression/composed"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.config().NexusEndpoint == "" {
		if _, leaked := composed["evolution_handoff"]; leaked {
			t.Fatalf("local-only service leaked Nexus evolution handoff: %#v", composed)
		}
		if next, _ := composed["next_required_action"].(string); !strings.Contains(next, "unavailable without Nexus") {
			t.Fatalf("local-only compose did not preserve Nexus boundary: %#v", composed)
		}
	} else {
		handoff, ok := composed["evolution_handoff"].(map[string]any)
		if !ok || !strings.Contains(handoff["statement"].(string), "on-demand Skill") {
			t.Fatalf("Nexus-enabled composition did not produce candidate-only handoff: %#v", composed)
		}
		if !strings.Contains(handoff["note"].(string), "does not write Recall") {
			t.Fatalf("evolution authority boundary missing: %#v", handoff)
		}
	}
}

func TestTaskManageImprovementSchemaPublishesBlindContract(t *testing.T) {
	schema := ManageInputSchema(config.Config{NexusEndpoint: "http://nexus.test"})
	props := schema["properties"].(map[string]any)
	for _, field := range []string{"improvement_experiment", "improvement_update"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("task_manage schema missing %s", field)
		}
	}
	actions := props["action"].(map[string]any)["enum"].([]string)
	for _, expected := range []string{"get_improvement_view", "improvement_update"} {
		found := false
		for _, action := range actions {
			if action == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("task_manage schema missing action %s: %#v", expected, actions)
		}
	}

	output := ManageOutputSchema(config.Config{NexusEndpoint: "http://nexus.test"})
	outProps := output["properties"].(map[string]any)
	for _, field := range []string{"improvement_view", "improvement_candidate", "evolution_handoff"} {
		if _, ok := outProps[field]; !ok {
			t.Fatalf("task_manage output schema missing %s", field)
		}
	}
}
