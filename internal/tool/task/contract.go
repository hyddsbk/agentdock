package task

import (
	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

const ToolTaskManage = "task_manage"

func ManageInputSchema(cfg config.Config) map[string]any {
	stringProp := toolcontract.String
	boundedIntProp := toolcontract.BoundedInteger
	props := map[string]any{
		"action":                map[string]any{"type": "string", "description": "Task lifecycle action. Use checkpoint to update progress and handoff to persist a fresh-session resume packet.", "enum": []string{"create", "list", "get", "checkpoint", "block", "resume", "handoff", "final_review", "complete"}},
		"task_id":               stringProp("Persistent task id for get, checkpoint, block, resume, handoff, final_review, or complete."),
		"title":                 stringProp("Short task title. Required for action=create."),
		"goal":                  stringProp("Fixed task goal. Required for action=create."),
		"completion_conditions": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "Conditions that must be true before final_review can pass. Required for action=create."},
		"requirement_refs":      map[string]any{"type": "array", "maxItems": 32, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Optional authoritative requirement/spec pointers for traceable work. Every ref must be mapped by at least one step; do not copy spec prose here."},
		"step_id":               stringProp("Task step id for a single-step checkpoint."),
		"tdd_seam":              tddSeamInputSchema(),
		"completed_step_ids":    map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Task step ids to mark completed in one atomic batch checkpoint."},
		"current_step_id":       stringProp("Single task step id to mark in_progress in a batch checkpoint."),
		"next_step_id":          stringProp("For handoff, the in-progress or dependency-ready step the next session should continue. Leave empty only for tasks with no steps."),
		"context_refs":          map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "For handoff, compact pointers to authoritative context the next session should load; do not duplicate the source content."},
		"status":                map[string]any{"type": "string", "description": "Action-specific status: task list filter, single-step checkpoint status, or final review status.", "enum": []string{"active", "blocked", "completed", "pending", "in_progress", "pass", "failed"}},
		"limit":                 boundedIntProp("Maximum tasks returned by list. Defaults to 50 and is capped at 200.", 1, 200),
		"summary":               stringProp("Current progress, blocker, resume, handoff orientation, or final review summary."),
		"diagnostic":            diagnosticEvidenceSchema(),
		"execution_evidence":    executionEvidenceSchema(),
		"execution_receipts":    executionReceiptsInputSchema(),
		"verified":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Facts verified during final_review. Required when status=pass."},
		"risks":                 map[string]any{"type": "array", "maxItems": 16, "items": map[string]any{"type": "string"}, "description": "Unresolved risks for handoff, or remaining risks for a failed final_review."},
		"intent_spec":           outcomeAxisSchema("Intent/spec compliance outcome. Required for final_review."),
		"engineering_policy":    outcomeAxisSchema("Engineering/policy compliance outcome. Required for final_review."),
	}

	if cfg.NexusEndpoint == "" {
		props["project"] = stringProp("Optional project identifier stored with the task.")
		props["device"] = stringProp("Optional device identifier stored with the task.")
		props["steps"] = map[string]any{
			"type": "array", "maxItems": 12, "description": "Concrete task steps.",
			"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title"}, "properties": map[string]any{"id": stringProp("Stable step id."), "title": stringProp("Human-readable step title."), "tdd_test_command": stringProp("Optional TDD gate; completion requires executor-issued Red, Green, and Regression receipts for this frozen command."), "execution_contract": executionContractInputSchema(), "depends_on": map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Step ids that must be completed before this step can start or complete."}, "completion_conditions": map[string]any{"type": "array", "maxItems": 4, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Checkable conditions that define when this step is complete. Required on every step when any dependency edge is used."}, "context_refs": map[string]any{"type": "array", "maxItems": 8, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Compact references to authoritative context such as recall://, skill://, artifact://, issue, commit, or task output pointers; do not duplicate source content."}, "requirement_refs": map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Requirement/spec pointers implemented or verified by this step; each must exist in top-level requirement_refs."}, "quality": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "outcome"}, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"discovery", "vertical_slice", "wide_refactor_expand", "wide_refactor_migrate", "wide_refactor_contract", "verification", "release", "diagnostic_feedback_loop", "diagnostic_reproduction", "diagnostic_hypothesis", "diagnostic_validation", "diagnostic_regression"}}, "outcome": stringProp("Independently observable outcome delivered by this step."), "refactor_group": stringProp("Stable group id for expand-migrate-contract wide refactors."), "dependency_reasons": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "One concise reason for every depends_on edge; keys must exactly match depends_on."}}}}},
		}
		return taskManageInputObject(props)
	}

	props["project"] = stringProp("Optional project identifier used to hard-scope Evolution guidance and evidence candidates. Omit only for global tasks.")
	props["device"] = stringProp("Optional device identifier used to hard-scope device-specific Evolution guidance and evidence candidates.")
	props["steps"] = map[string]any{
		"type": "array", "maxItems": 12, "description": "Concrete task steps. Required when composing multiple source templates.",
		"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title"}, "properties": map[string]any{"id": stringProp("Stable step id."), "title": stringProp("Human-readable step title."), "tdd_test_command": stringProp("Optional TDD gate; completion requires executor-issued Red, Green, and Regression receipts for this frozen command."), "execution_contract": executionContractInputSchema(), "depends_on": map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Step ids that must be completed before this step can start or complete."}, "completion_conditions": map[string]any{"type": "array", "maxItems": 4, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Checkable conditions that define when this step is complete. Required on every step when any dependency edge is used."}, "context_refs": map[string]any{"type": "array", "maxItems": 8, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Compact references to authoritative context such as recall://, skill://, artifact://, issue, commit, or task output pointers; do not duplicate source content."}, "requirement_refs": map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Requirement/spec pointers implemented or verified by this step; each must exist in top-level requirement_refs."}, "quality": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "outcome"}, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"discovery", "vertical_slice", "wide_refactor_expand", "wide_refactor_migrate", "wide_refactor_contract", "verification", "release", "diagnostic_feedback_loop", "diagnostic_reproduction", "diagnostic_hypothesis", "diagnostic_validation", "diagnostic_regression"}}, "outcome": stringProp("Independently observable outcome delivered by this step."), "refactor_group": stringProp("Stable group id for expand-migrate-contract wide refactors."), "dependency_reasons": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "One concise reason for every depends_on edge; keys must exactly match depends_on."}}}}},
	}
	props["template_id"] = stringProp("Single active workflow template to apply. Its current active version is resolved automatically.")
	props["source_template_ids"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 3, "items": map[string]any{"type": "string"}, "description": "Two or three templates already composed by the model into steps and completion_conditions."}
	props["learning_checks"] = map[string]any{
		"type": "array", "maxItems": 3, "description": "Advanced create-only blinded validation checks. Bind these Evolution ids before Guidance is generated; support-bearing targets are withheld from this Task's Guidance and may be assessed only from its frozen final_review.",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"evolution_id", "on_success", "on_failure"},
			"properties": map[string]any{
				"evolution_id": stringProp("Evolution id intentionally selected for this pre-execution validation."),
				"on_success":   map[string]any{"type": "string", "enum": []string{"support", "contradict", "none"}},
				"on_failure":   map[string]any{"type": "string", "enum": []string{"support", "contradict", "none"}},
			},
		},
	}
	return taskManageInputObject(props)
}

func diagnosticEvidenceSchema() map[string]any {
	return map[string]any{
		"type": "object", "description": "Structured evidence for a diagnostic checkpoint. Accepted only by single-step checkpoint; stage-specific fields are validated by the Task runtime.", "additionalProperties": false,
		"properties": map[string]any{
			"command":       map[string]any{"type": "string", "description": "Agent-runnable command or deterministic action used for the feedback loop, minimal reproduction, or regression."},
			"observed":      map[string]any{"type": "string", "enum": []string{"red", "green"}, "description": "Observed feedback-loop state."},
			"symptom":       map[string]any{"type": "string", "description": "Concrete failing symptom reproduced by the initial feedback loop."},
			"repro_ref":     map[string]any{"type": "string", "description": "Reference to the minimized reproduction fixture, case, script, or artifact."},
			"hypothesis":    map[string]any{"type": "string", "description": "Specific causal hypothesis."},
			"falsifier":     map[string]any{"type": "string", "description": "Observation that would falsify the hypothesis."},
			"variable":      map[string]any{"type": "string", "description": "Single variable changed by this validation."},
			"method":        map[string]any{"type": "string", "description": "How the single variable was changed and checked."},
			"outcome":       map[string]any{"type": "string", "enum": []string{"supported", "refuted"}, "description": "Result of the single-variable validation."},
			"evidence_refs": map[string]any{"type": "array", "maxItems": 8, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Compact references to evidence; do not embed raw logs, HAR files, credentials, or source dumps."},
		},
	}
}

func executionEvidenceSchema() map[string]any {
	return map[string]any{
		"type": "object", "description": "Compact implementation/verification evidence for a traceable step checkpoint. Raw logs and source dumps belong in referenced artifacts.", "additionalProperties": false,
		"required": []string{"evidence_refs"},
		"properties": map[string]any{
			"evidence_refs": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Pointers such as commit://, test://, artifact://, issue://, or receipt:// that prove the mapped requirements were implemented or verified."},
		},
	}
}

func executionReceiptsInputSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": 1, "maxItems": 8,
		"description": "Executor-issued receipt references for a step with a declared execution_contract. Each check_id must map exactly once to the frozen check.",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"check_id", "ref"},
			"properties": map[string]any{
				"check_id": map[string]any{"type": "string"},
				"ref":      map[string]any{"type": "string", "description": "exec:// receipt reference returned by exec_command."},
			},
		},
	}
}

func executionContractInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"checks"},
		"description": "Optional generic executor-attested completion gate. Unlike TDD, each check has one frozen command and allowed terminal exit codes.",
		"properties": map[string]any{
			"ordered": map[string]any{"type": "boolean", "description": "When true, checks must execute in declared order with no overlap."},
			"checks": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 8,
				"items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"id", "command"},
					"properties": map[string]any{
						"id":      map[string]any{"type": "string"},
						"command": map[string]any{"type": "string"},
						"allowed_exit_codes": map[string]any{
							"type": "array", "maxItems": 8, "uniqueItems": true,
							"items":       map[string]any{"type": "integer"},
							"description": "Acceptable exit codes. Omit to require exit code 0.",
						},
					},
				},
			},
		},
	}
}

func outcomeAxisSchema(description string) map[string]any {
	return map[string]any{
		"type": "object", "description": description, "additionalProperties": false, "required": []string{"status"},
		"properties": map[string]any{
			"status":   map[string]any{"type": "string", "enum": []string{"pass", "failed"}},
			"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"findings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

func taskManageInputObject(props map[string]any) map[string]any {
	schema := toolcontract.InputObject(props, "action")
	schema["allOf"] = []any{
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "create"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"title", "goal", "completion_conditions"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "handoff"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"task_id", "summary"}},
		},
	}
	return schema
}

func ManageOutputSchema(cfg config.Config) map[string]any {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	arrayProp := toolcontract.ObjectArray
	objectProp := toolcontract.OpenObject
	props := map[string]any{
		"action":               stringProp("Completed task action."),
		"task_id":              stringProp("Persistent task id returned by create and usable with task lifecycle actions."),
		"task":                 objectProp("Full persistent task state returned only by get."),
		"task_summary":         objectProp("Compact task summary returned by task lifecycle actions."),
		"review_status":        stringProp("Final review status when present: not_started, pass, or failed."),
		"final_review":         objectProp("Compact final review state with status and counts."),
		"tasks":                arrayProp("Compact task summaries ordered by most recent update."),
		"count":                intProp("Returned item count."),
		"state_dir":            stringProp("Local AgentDock task state directory."),
		"next_required_action": stringProp("Concise guidance for checkpoint progress, handoff/resume, or final review."),
		"resume_context":       objectProp("Derived fresh-session resume packet returned by handoff and get when a current handoff exists."),
	}
	if cfg.NexusEndpoint != "" {
		props["next_required_action"] = stringProp("Concise guidance for checkpoint progress, handoff/resume, template composition, or final review.")
		props["guidance_context"] = arrayProp("Mature evolution records automatically recalled before task execution.")
		props["review_revision"] = stringProp("Immutable final_review revision used to bind evolution evidence.")
		props["evolution_candidates"] = arrayProp("Read-only candidate experiences that the saved final_review may verify.")
		props["evolution_warning"] = stringProp("Non-blocking evolution-side warning; Task lifecycle still succeeded.")
	}
	return toolcontract.OutputObject(props)
}

func InputSchema(name string, cfg config.Config) (map[string]any, bool) {
	if name != ToolTaskManage {
		return nil, false
	}
	return ManageInputSchema(cfg), true
}

func OutputSchema(name string, cfg config.Config) (map[string]any, bool) {
	if name != ToolTaskManage {
		return nil, false
	}
	return ManageOutputSchema(cfg), true
}
