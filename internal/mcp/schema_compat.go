package mcp

import "encoding/json"

// openAICompatibleSchemaProjection returns a transport-only projection of a
// tool JSON Schema that avoids composition keywords rejected by OpenAI's
// strict tool/Structured Outputs schema subset.
//
// AgentDock's runtime remains the authoritative validator. This projection
// intentionally broadens only the published MCP schema when a constraint
// cannot be represented in the supported subset; server-side validation still
// fails closed for action-specific requirements and invariants.
func openAICompatibleSchemaProjection(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		projected, _ := projectOpenAICompatibleSchemaValue(schema).(map[string]any)
		return projected
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		projected, _ := projectOpenAICompatibleSchemaValue(schema).(map[string]any)
		return projected
	}
	projected, _ := projectOpenAICompatibleSchemaValue(normalized).(map[string]any)
	return projected
}

func projectOpenAICompatibleSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		projected := make(map[string]any, len(typed))
		for key, child := range typed {
			switch key {
			case "allOf", "not", "dependentRequired", "dependentSchemas", "if", "then", "else":
				// These composition keywords are unsupported by OpenAI's
				// strict JSON Schema subset. The corresponding invariants are
				// still enforced by AgentDock's runtime validators.
				continue
			case "const":
				// A one-value enum preserves the public constraint while
				// staying inside the supported tool-schema subset.
				projected["enum"] = []any{projectOpenAICompatibleSchemaValue(child)}
			default:
				projected[key] = projectOpenAICompatibleSchemaValue(child)
			}
		}
		return projected
	case []any:
		projected := make([]any, len(typed))
		for i, child := range typed {
			projected[i] = projectOpenAICompatibleSchemaValue(child)
		}
		return projected
	default:
		return value
	}
}
