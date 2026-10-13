package mcp

import (
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func TestToolDescriptorsProjectOpenAICompatibleJSONSchema(t *testing.T) {
	definition := ToolDefinition{
		Name:        "compat_probe",
		Title:       "Compatibility probe",
		Description: "Exercises the public MCP schema projection.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "const": "run"},
			},
			"allOf": []any{
				map[string]any{
					"if": map[string]any{
						"properties": map[string]any{
							"action": map[string]any{"const": "run"},
						},
					},
					"then": map[string]any{"required": []string{"action"}},
				},
			},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "ok"},
			},
		},
	}

	descriptor := toolDescriptors([]ToolDefinition{definition}, false)[0]
	for _, field := range []string{"inputSchema", "outputSchema"} {
		assertNoUnsupportedOpenAIComposition(t, field, descriptor[field])
	}

	input := descriptor["inputSchema"].(map[string]any)
	action := input["properties"].(map[string]any)["action"].(map[string]any)
	enum, ok := action["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "run" {
		t.Fatalf("projected action enum = %#v, want [run]", action["enum"])
	}
}

func TestTaskManagePublishedSchemaAvoidsUnsupportedOpenAIComposition(t *testing.T) {
	descriptor := toolDescriptorsForConfig(t, []string{"task_manage"}, config.Config{NexusEndpoint: "http://127.0.0.1:18777"})[0]
	assertNoUnsupportedOpenAIComposition(t, "task_manage.inputSchema", descriptor["inputSchema"])
	assertNoUnsupportedOpenAIComposition(t, "task_manage.outputSchema", descriptor["outputSchema"])
}

func TestAllPublishedToolDescriptorsAvoidUnsupportedOpenAIComposition(t *testing.T) {
	for _, descriptor := range toolDescriptors(app.ToolDefinitions(), true) {
		name, _ := descriptor["name"].(string)
		assertNoUnsupportedOpenAIComposition(t, name+".inputSchema", descriptor["inputSchema"])
		assertNoUnsupportedOpenAIComposition(t, name+".outputSchema", descriptor["outputSchema"])
	}
}

func assertNoUnsupportedOpenAIComposition(t *testing.T, path string, value any) {
	t.Helper()
	blocked := map[string]bool{
		"allOf": true, "not": true, "dependentRequired": true, "dependentSchemas": true,
		"if": true, "then": true, "else": true, "const": true,
	}
	var walk func(string, any)
	walk = func(current string, item any) {
		switch typed := item.(type) {
		case map[string]any:
			for key, child := range typed {
				if blocked[key] {
					t.Fatalf("%s contains unsupported OpenAI JSON Schema keyword %q", current, key)
				}
				walk(current+"."+key, child)
			}
		case []any:
			for i, child := range typed {
				walk(current, child)
				_ = i
			}
		}
	}
	walk(path, value)
}
