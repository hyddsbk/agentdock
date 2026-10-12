package task

import (
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestTaskManageSchemaExposesTwoAxisAndTDDContracts(t *testing.T) {
	for _, cfg := range []config.Config{{}, {NexusEndpoint: "http://nexus.test"}} {
		schema := ManageInputSchema(cfg)
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("properties missing: %#v", schema)
		}
		for _, field := range []string{"intent_spec", "engineering_policy", "tdd_seam"} {
			if _, ok := props[field]; !ok {
				t.Fatalf("task_manage schema missing %s with Nexus=%t", field, cfg.NexusEndpoint != "")
			}
		}
		tdd, ok := props["tdd_seam"].(map[string]any)
		if !ok {
			t.Fatalf("tdd_seam schema = %#v", props["tdd_seam"])
		}
		required, ok := tdd["required"].([]string)
		if !ok || len(required) != 3 {
			t.Fatalf("tdd_seam required = %#v", tdd["required"])
		}
		steps := props["steps"].(map[string]any)
		items := steps["items"].(map[string]any)
		stepProps := items["properties"].(map[string]any)
		if _, ok := stepProps["tdd_test_command"]; !ok {
			t.Fatalf("step schema missing tdd_test_command with Nexus=%t", cfg.NexusEndpoint != "")
		}
	}
}
