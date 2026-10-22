package app

import (
	mcpcontract "github.com/uvwt/agentdock-protocol/mcpcontract"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/evolution"
	toolacp "github.com/uvwt/agentdock/internal/tool/acp"
	toolbrowser "github.com/uvwt/agentdock/internal/tool/browser"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	toolmedia "github.com/uvwt/agentdock/internal/tool/media"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

func staticToolContract(
	name string,
	input func(string) (map[string]any, bool),
	output func(string) (map[string]any, bool),
) (ToolContract, bool) {
	inputSchema, ok := input(name)
	if !ok {
		return ToolContract{}, false
	}
	outputSchema, ok := output(name)
	if !ok {
		return ToolContract{}, false
	}
	return ToolContract{InputSchema: inputSchema, OutputSchema: outputSchema}, true
}

func canonicalToolContract(name string, _ config.Config) (ToolContract, bool) {
	inputSchema, ok := mcpcontract.InputSchema(name)
	if !ok {
		return ToolContract{}, false
	}
	if name == mcpcontract.ToolAgentDockContext {
		return ToolContract{InputSchema: inputSchema, OutputSchema: mcpcontract.LocalAgentDockContextOutputSchema()}, true
	}
	outputSchema, ok := mcpcontract.OutputSchema(name)
	if !ok {
		return ToolContract{}, false
	}
	return ToolContract{InputSchema: inputSchema, OutputSchema: outputSchema}, true
}

func workflowTaskContract(name string, cfg config.Config) (ToolContract, bool) {
	contract, ok := canonicalToolContract(name, cfg)
	if !ok || name != "workflow_template_manage" {
		return contract, ok
	}
	inputProps, ok := contract.InputSchema["properties"].(map[string]any)
	if !ok {
		return ToolContract{}, false
	}
	inputProps["admission_ref"] = map[string]any{
		"type":        "string",
		"description": "Passing frozen Task Admission reference. Required for action=match.",
	}
	allOf, _ := contract.InputSchema["allOf"].([]any)
	contract.InputSchema["allOf"] = append(allOf, map[string]any{
		"if": map[string]any{
			"properties": map[string]any{"action": map[string]any{"const": "match"}},
			"required":   []string{"action"},
		},
		"then": map[string]any{"required": []string{"admission_ref"}},
	})
	outputProps, ok := contract.OutputSchema["properties"].(map[string]any)
	if !ok {
		return ToolContract{}, false
	}
	for name, description := range map[string]string{
		"admission_ref":         "Frozen Task Admission reference consumed by workflow matching.",
		"admission_digest":      "SHA-256 digest of the frozen Admission contract.",
		"workflow_match_digest": "SHA-256 digest of the persisted workflow-match receipt.",
	} {
		outputProps[name] = map[string]any{"type": "string", "description": description}
	}
	return contract, true
}

func fileToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolfile.InputSchema, toolfile.OutputSchema)
}

func commandToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolcommand.InputSchema, toolcommand.OutputSchema)
}

func taskToolContract(name string, cfg config.Config) (ToolContract, bool) {
	inputSchema, ok := tooltask.InputSchema(name, cfg)
	if !ok {
		return ToolContract{}, false
	}
	outputSchema, ok := tooltask.OutputSchema(name, cfg)
	if !ok {
		return ToolContract{}, false
	}
	return ToolContract{InputSchema: inputSchema, OutputSchema: outputSchema}, true
}

func evolutionToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, evolution.InputSchema, evolution.OutputSchema)
}

func acpToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolacp.InputSchema, toolacp.OutputSchema)
}

func skillToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolskill.InputSchema, toolskill.OutputSchema)
}

func mcpToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolmcp.InputSchema, toolmcp.OutputSchema)
}

func mediaToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolmedia.InputSchema, toolmedia.OutputSchema)
}

func browserToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, toolbrowser.InputSchema, toolbrowser.OutputSchema)
}
