package task

import (
	"testing"

	"github.com/uvwt/agentdock/internal/config"
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
