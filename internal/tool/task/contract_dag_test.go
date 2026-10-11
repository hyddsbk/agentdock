package task

import (
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskManageStepSchemaExposesDependencies(t *testing.T) {
	for _, cfg := range []config.Config{{}, {NexusEndpoint: "http://127.0.0.1:18777"}} {
		schema := ManageInputSchema(cfg)
		properties := schema["properties"].(map[string]any)
		steps := properties["steps"].(map[string]any)
		item := steps["items"].(map[string]any)
		stepProperties := item["properties"].(map[string]any)
		dependencySchema, ok := stepProperties["depends_on"].(map[string]any)
		if !ok {
			t.Fatalf("task step schema missing depends_on: %#v", stepProperties)
		}
		if dependencySchema["type"] != "array" || dependencySchema["uniqueItems"] != true {
			t.Fatalf("depends_on schema = %#v", dependencySchema)
		}
		qualitySchema, ok := stepProperties["quality"].(map[string]any)
		if !ok || qualitySchema["type"] != "object" {
			t.Fatalf("task step schema missing quality: %#v", stepProperties)
		}
		for _, axis := range []string{"intent_spec", "engineering_policy"} {
			if _, ok := properties[axis].(map[string]any); !ok {
				t.Fatalf("task schema missing %s", axis)
			}
		}
	}
}

func TestNormalizeTaskManageRejectsDiagnosticOutsideCheckpoint(t *testing.T) {
	_, err := normalizeTaskManageRequest(ManageRequest{
		Action:     "create",
		Diagnostic: &taskstate.DiagnosticEvidence{Command: "repro", Observed: taskstate.DiagnosticObservedRed},
	})
	if err == nil {
		t.Fatal("diagnostic evidence outside checkpoint was accepted")
	}
}

func TestTaskManageSchemaExposesDiagnosticFeedbackLoopContract(t *testing.T) {
	for _, cfg := range []config.Config{{}, {NexusEndpoint: "http://127.0.0.1:18777"}} {
		schema := ManageInputSchema(cfg)
		properties := schema["properties"].(map[string]any)
		diagnostic, ok := properties["diagnostic"].(map[string]any)
		if !ok || diagnostic["type"] != "object" || diagnostic["additionalProperties"] != false {
			t.Fatalf("task schema missing strict diagnostic evidence: %#v", properties["diagnostic"])
		}
		diagnosticProperties := diagnostic["properties"].(map[string]any)
		for _, field := range []string{"command", "observed", "symptom", "repro_ref", "hypothesis", "falsifier", "variable", "method", "outcome", "evidence_refs"} {
			if _, ok := diagnosticProperties[field]; !ok {
				t.Fatalf("diagnostic schema missing %s", field)
			}
		}

		steps := properties["steps"].(map[string]any)
		stepProperties := steps["items"].(map[string]any)["properties"].(map[string]any)
		qualityProperties := stepProperties["quality"].(map[string]any)["properties"].(map[string]any)
		kinds := qualityProperties["kind"].(map[string]any)["enum"].([]string)
		for _, want := range []string{"diagnostic_feedback_loop", "diagnostic_reproduction", "diagnostic_hypothesis", "diagnostic_validation", "diagnostic_regression"} {
			found := false
			for _, kind := range kinds {
				if kind == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("quality kind enum missing %q: %v", want, kinds)
			}
		}
	}
}
