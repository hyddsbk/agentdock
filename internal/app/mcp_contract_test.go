package app

import (
	"reflect"
	"testing"

	"github.com/uvwt/agentdock-protocol/mcpcontract"
)

func TestCanonicalToolDefinitionsMatchSharedContract(t *testing.T) {
	definitions := make(map[string]ToolDefinition, len(mcpcontract.ToolNames()))
	for _, definition := range ToolDefinitions() {
		if mcpcontract.IsCanonicalTool(definition.Name) {
			definitions[definition.Name] = definition
		}
	}
	if len(definitions) != len(mcpcontract.ToolNames()) {
		t.Fatalf("canonical tool count=%d want=%d", len(definitions), len(mcpcontract.ToolNames()))
	}

	for _, name := range mcpcontract.ToolNames() {
		definition, ok := definitions[name]
		if !ok {
			t.Fatalf("canonical tool %s missing", name)
		}
		wantInput, _ := mcpcontract.InputSchema(name)
		if name == "workflow_template_manage" {
			props := wantInput["properties"].(map[string]any)
			props["admission_ref"] = map[string]any{
				"type":        "string",
				"description": "Passing frozen Task Admission reference. Required for action=match.",
			}
			allOf, _ := wantInput["allOf"].([]any)
			wantInput["allOf"] = append(allOf, map[string]any{
				"if": map[string]any{
					"properties": map[string]any{"action": map[string]any{"const": "match"}},
					"required":   []string{"action"},
				},
				"then": map[string]any{"required": []string{"admission_ref"}},
			})
		}
		if !reflect.DeepEqual(definition.InputSchema, wantInput) {
			t.Fatalf("%s input schema drifted from shared contract", name)
		}
		var wantOutput map[string]any
		if name == mcpcontract.ToolAgentDockContext {
			wantOutput = mcpcontract.LocalAgentDockContextOutputSchema()
		} else {
			wantOutput, _ = mcpcontract.OutputSchema(name)
		}
		if name == "workflow_template_manage" {
			props := wantOutput["properties"].(map[string]any)
			for field, description := range map[string]string{
				"admission_ref":         "Frozen Task Admission reference consumed by workflow matching.",
				"admission_digest":      "SHA-256 digest of the frozen Admission contract.",
				"workflow_match_digest": "SHA-256 digest of the persisted workflow-match receipt.",
			} {
				props[field] = map[string]any{"type": "string", "description": description}
			}
		}
		if !reflect.DeepEqual(definition.OutputSchema, wantOutput) {
			t.Fatalf("%s output schema drifted from shared contract", name)
		}

		wantAnnotations, _ := mcpcontract.AnnotationContract(name)
		annotations := definition.Annotations
		if annotations == nil ||
			annotations.ReadOnlyHint != wantAnnotations.ReadOnlyHint ||
			!reflect.DeepEqual(annotations.DestructiveHint, wantAnnotations.DestructiveHint) ||
			!reflect.DeepEqual(annotations.OpenWorldHint, wantAnnotations.OpenWorldHint) {
			t.Fatalf("%s annotations drifted: got=%#v want=%#v", name, annotations, wantAnnotations)
		}
		wantIdempotent := wantAnnotations.IdempotentHint != nil && *wantAnnotations.IdempotentHint
		if annotations.IdempotentHint != wantIdempotent {
			t.Fatalf("%s idempotentHint=%v want=%v", name, annotations.IdempotentHint, wantIdempotent)
		}
	}
}
