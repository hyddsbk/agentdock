package task

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func tddSeamInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"red_ref", "green_ref", "regression_ref"},
		"properties": map[string]any{
			"red_ref":        toolcontract.String("Executor-issued exec:// receipt for the failing Red run."),
			"green_ref":      toolcontract.String("Executor-issued exec:// receipt for the passing Green run."),
			"regression_ref": toolcontract.String("Executor-issued exec:// receipt for the passing regression run."),
		},
	}
}
