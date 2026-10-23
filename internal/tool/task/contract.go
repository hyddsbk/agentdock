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
		"action":                   map[string]any{"type": "string", "description": "Task admission, lifecycle, recursive-improvement, and read-only audit action. Use admit before workflow matching and create; use improvement_update only for Tasks created with improvement_experiment.", "enum": []string{"admit", "get_admission", "create", "list", "get", "get_improvement_view", "improvement_update", "backtrace", "revalidate", "export_audit_bundle", "verify_audit_bundle", "export_signed_audit_bundle", "verify_signed_audit_bundle", "verify_signed_audit_bundle_online", "checkpoint", "block", "resume", "handoff", "final_review", "complete"}},
		"admission":                admissionInputSchema(),
		"admission_ref":            stringProp("Immutable admission reference. Required for get_admission and create; workflow matching uses the same reference."),
		"improvement_experiment":   improvementExperimentInputSchema(),
		"improvement_update":       improvementUpdateInputSchema(),
		"task_id":                  stringProp("Persistent task id for get, backtrace scope, revalidate, export_audit_bundle, checkpoint, block, resume, handoff, final_review, or complete."),
		"title":                    stringProp("Short task title. Required for action=create."),
		"goal":                     stringProp("Optional create-time echo of the frozen admission goal. When supplied it must match exactly."),
		"completion_conditions":    map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "Optional create-time echo of frozen admission success_conditions. When supplied it must match exactly."},
		"requirement_refs":         map[string]any{"type": "array", "maxItems": 32, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Optional authoritative requirement/spec pointers for traceable work. Every ref must be mapped by at least one step; do not copy spec prose here."},
		"step_id":                  stringProp("Task step id for a single-step checkpoint."),
		"tdd_seam":                 tddSeamInputSchema(),
		"completed_step_ids":       map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Task step ids to mark completed in one atomic batch checkpoint."},
		"current_step_id":          stringProp("Single task step id to mark in_progress in a batch checkpoint."),
		"next_step_id":             stringProp("For handoff, the in-progress or dependency-ready step the next session should continue. Leave empty only for tasks with no steps."),
		"context_refs":             map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "For handoff, compact pointers to authoritative context the next session should load; do not duplicate the source content."},
		"status":                   map[string]any{"type": "string", "description": "Action-specific status: task list filter, single-step checkpoint status, or final review status.", "enum": []string{"active", "blocked", "completed", "pending", "in_progress", "pass", "failed"}},
		"limit":                    boundedIntProp("Maximum tasks returned by list. Defaults to 50 and is capped at 200.", 1, 200),
		"summary":                  stringProp("Current progress, blocker, resume, handoff orientation, or final review summary."),
		"diagnostic":               diagnosticEvidenceSchema(),
		"execution_evidence":       executionEvidenceSchema(),
		"evidence_records":         evidenceRecordsInputSchema(),
		"unknown_updates":          unknownUpdatesInputSchema(),
		"execution_receipts":       executionReceiptsInputSchema(),
		"verified":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Facts verified during final_review. Required when status=pass."},
		"verified_evidence":        reviewEvidenceBindingsSchema("Server-validated bindings for every verified fact in a passing final_review."),
		"acknowledged_unknown_ids": map[string]any{"type": "array", "maxItems": 64, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Open non-blocking unknown ids explicitly carried through a passing final_review. Blocking unknowns can never be acknowledged past the gate."},
		"risks":                    map[string]any{"type": "array", "maxItems": 16, "items": map[string]any{"type": "string"}, "description": "Unresolved risks for handoff, or remaining risks for a failed final_review."},
		"intent_spec":              outcomeAxisSchema("Intent/spec compliance outcome. Required for final_review."),
		"engineering_policy":       outcomeAxisSchema("Engineering/policy compliance outcome. Required for final_review."),
		"provenance_query":         provenanceQuerySchema(cfg.NexusEndpoint != ""),
		"review_revision":          stringProp("Optional immutable final-review revision pin for revalidate/export_audit_bundle/export_signed_audit_bundle. A mismatch fails closed."),
		"audit_bundle":             portableAuditBundleInputSchema(),
		"audit_signature":          openAuditObjectSchema("Detached audit signature produced by the trusted AgentDock signer."),
		"audit_trust_registry":     openAuditObjectSchema("Public root-signed audit trust registry used for independent verification."),
		"audit_trust_checkpoint":   openAuditObjectSchema("Root-signed audit trust checkpoint used as the freshness authority for offline verification."),
		"expected_root_key_id":     stringProp("Externally pinned audit root key id for explicit-pin signed audit verification. Online checkpoint verification uses the bootstrapped verifier trust state instead."),
		"expected_trust_revision":  boundedIntProp("Legacy P9 externally pinned minimum audit trust-registry revision. Omit when audit_trust_checkpoint supplies freshness.", 1, 2147483647),
		"expected_bundle_sha256":   stringProp("Optional externally pinned P8 bundle SHA-256 for audit bundle verification actions."),
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

func admissionInputSchema() map[string]any {
	stringArray := func(description string, minItems int) map[string]any {
		schema := map[string]any{"type": "array", "maxItems": 32, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": description}
		if minItems > 0 {
			schema["minItems"] = minItems
		}
		return schema
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required":    []string{"goal", "deliverable", "audience", "success_conditions", "failure_conditions", "evidence_requirement", "mutation_authority", "complexity"},
		"description": "Standards-first admission contract frozen before workflow matching and task creation.",
		"properties": map[string]any{
			"goal":                 map[string]any{"type": "string"},
			"deliverable":          map[string]any{"type": "string"},
			"audience":             map[string]any{"type": "string"},
			"success_conditions":   stringArray("Conditions that define successful delivery.", 1),
			"failure_conditions":   stringArray("Conditions that make execution unacceptable or failed.", 1),
			"constraints":          stringArray("Hard constraints and red lines.", 0),
			"evidence_requirement": map[string]any{"type": "string", "enum": []string{"none", "lightweight", "research", "first_party", "execution_receipt"}},
			"mutation_authority":   map[string]any{"type": "string", "enum": []string{"read_only", "local_write", "staging_write", "production_write"}},
			"complexity":           map[string]any{"type": "string", "enum": []string{"trivial", "standard", "substantial", "high_risk"}},
			"workflow_type":        map[string]any{"type": "string", "description": "Optional frozen workflow type hint used by workflow template matching."},
			"decision_frontier":    stringArray("Unresolved decisions. Any entry makes admission status resolve.", 0),
			"blocked_reasons":      stringArray("Known blockers. Any entry makes admission status blocked.", 0),
			"project":              map[string]any{"type": "string"},
			"device":               map[string]any{"type": "string"},
		},
	}
}

func improvementExperimentInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required":    []string{"target", "mode", "probe_criteria"},
		"description": "Optional create-time recursive improvement contract owned by this Task. It freezes the target, parallel/sequential refinement mode, probe criteria, cost-aware harness surface policy, and blind evaluation boundary.",
		"properties": map[string]any{
			"target":         map[string]any{"type": "string", "description": "Harness, Skill, workflow, policy, or other first-party target being improved."},
			"mode":           map[string]any{"type": "string", "enum": []string{"parallel", "sequential"}, "description": "Parallel explores independent alternatives; sequential refines only after the parent candidate has been probed."},
			"probe_criteria": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Frozen criteria used to compare candidate behavior on the same task/evidence boundary."},
			"surface_policy": map[string]any{"type": "string", "enum": []string{"cost_aware_v1"}, "description": "Instruction-placement policy. Defaults to cost_aware_v1: prefer on-demand Skill/helper/subagent/runtime enforcement over always-on prose when appropriate."},
		},
	}
}

func improvementUpdateInputSchema() map[string]any {
	evidenceRefs := map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "uniqueItems": true, "items": map[string]any{"type": "string"}}
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"action"},
		"description": "One mutation of a Task-owned recursive improvement experiment. Candidate authoring/probing uses internal ids; blind selection must use only aliases returned by get_improvement_view.",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"register_candidate", "record_probe", "select", "compose"}},
			"candidate": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"scope", "description", "artifact_ref", "blind_artifact_ref"},
				"properties": map[string]any{
					"scope":              map[string]any{"type": "string", "enum": []string{"base", "invariant", "project", "alternative"}, "description": "Candidate scope. Keep general/invariant changes separate from project-specific changes."},
					"description":        map[string]any{"type": "string", "description": "Author-facing candidate description. This is intentionally omitted from the blind evaluator view."},
					"artifact_ref":       map[string]any{"type": "string", "description": "Author/provenance pointer to the candidate harness/artifact; never expose it to the blind evaluator."},
					"blind_artifact_ref": map[string]any{"type": "string", "description": "Separately staged identity-neutral evaluator bundle. It must differ from artifact_ref and contain no base/new/scope metadata."},
					"parent_id":          map[string]any{"type": "string", "description": "Sequential mode only. A later refinement must reference a previously probed candidate."},
				},
			},
			"probe": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"candidate_id", "summary", "evidence_refs"},
				"properties": map[string]any{
					"candidate_id":  map[string]any{"type": "string"},
					"summary":       map[string]any{"type": "string", "description": "Observed probe behavior against the frozen criteria; evidence is data, not executable instruction."},
					"evidence_refs": evidenceRefs,
				},
			},
			"selection": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"aliases", "rationale", "evidence_refs"},
				"properties": map[string]any{
					"aliases":       map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Blind aliases only; do not use candidate ids, scopes, or author descriptions during evaluation."},
					"rationale":     map[string]any{"type": "string"},
					"evidence_refs": evidenceRefs,
				},
			},
			"composition": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"summary", "artifact_ref", "evidence_refs"},
				"properties": map[string]any{
					"summary":       map[string]any{"type": "string"},
					"lesson":        map[string]any{"type": "string", "description": "Optional reusable lesson. This remains a candidate only; learning still requires the existing evolve lifecycle."},
					"artifact_ref":  map[string]any{"type": "string"},
					"evidence_refs": evidenceRefs,
				},
			},
		},
	}
}

func openAuditObjectSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": true,
	}
}

func provenanceQuerySchema(withNexus bool) map[string]any {
	kinds := []string{"final_review_claim", "requirement", "exec_receipt"}
	description := "Read-only provenance root for action=backtrace. Resolves one Final Review claim, requirement ref, or exec:// receipt into the same audit graph."
	properties := map[string]any{
		"kind":            map[string]any{"type": "string", "enum": kinds},
		"ref":             map[string]any{"type": "string", "description": "Exact root reference. Final Review claims accept canonical task:<id>:review:<revision>:<scope>:<index> refs or exact claim text when task_id is supplied."},
		"task_id":         map[string]any{"type": "string", "description": "Optional hard task scope; required for requirement refs and plain-text Final Review claims."},
		"review_revision": map[string]any{"type": "string", "description": "Optional immutable final-review revision scope. A mismatch fails closed."},
	}
	if withNexus {
		kinds = append(kinds, "evolution_evidence")
		properties["kind"].(map[string]any)["enum"] = kinds
		properties["evolution_id"] = map[string]any{"type": "string", "description": "Optional hard Evolution scope. Required only to disambiguate an evidence ref consumed by multiple Evolution records."}
		description = "Read-only provenance root for action=backtrace. Resolves one Final Review claim, Evolution evidence ref, requirement ref, or exec:// receipt into the same audit graph."
	}
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"kind", "ref"},
		"description": description,
		"properties":  properties,
	}
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
		"type": "object", "description": "Compact opaque implementation/verification evidence for a traceable step checkpoint. Use commit://, test://, artifact://, issue:// or similar refs. exec:// is reserved for executor-attested execution_receipts and is rejected here.", "additionalProperties": false,
		"required": []string{"evidence_refs"},
		"properties": map[string]any{
			"evidence_refs": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Opaque outcome pointers such as commit://, test://, artifact://, or issue://. Do not place exec:// receipt identities here; supply them through execution_receipts so AgentDock can claim and attest them."},
		},
	}
}

func evidenceRecordsInputSchema() map[string]any {
	return map[string]any{
		"type": "array", "maxItems": 16,
		"description": "Provider-neutral semantic evidence appended atomically to one Task step. IDs and timestamps are server-owned.",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"statement", "fact_status", "coverage"},
			"properties": map[string]any{
				"statement":         map[string]any{"type": "string", "description": "One evidence-backed statement; keep observations separate from inference."},
				"fact_status":       map[string]any{"type": "string", "enum": []string{"observed", "derived", "inferred", "unknown", "contradicted"}, "description": "Semantic status of the statement."},
				"source_refs":       map[string]any{"type": "array", "maxItems": 16, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Opaque source/evidence pointers supporting this record. Required for every fact_status except unknown."},
				"coverage":          map[string]any{"type": "string", "enum": []string{"complete", "partial", "unknown"}, "description": "Coverage of the evidence producer for this statement. Missing or partial coverage is not absence."},
				"limitations":       map[string]any{"type": "array", "maxItems": 8, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Known boundaries or caveats that constrain the statement."},
				"untrusted_content": map[string]any{"type": "boolean", "description": "True when source material may contain target-controlled instructions or other untrusted content."},
				"partial_result":    map[string]any{"type": "boolean", "description": "True when the producer returned useful but incomplete output. Cannot be combined with complete coverage."},
			},
		},
	}
}

func unknownUpdatesInputSchema() map[string]any {
	return map[string]any{
		"type": "array", "maxItems": 16,
		"description": "Revision-aware updates to the Task-owned unknown ledger. Opening records uncertainty; resolving requires current revision plus evidence owned by this step.",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"action"},
			"properties": map[string]any{
				"action":            map[string]any{"type": "string", "enum": []string{"open", "resolve"}},
				"unknown_id":        map[string]any{"type": "string", "description": "Server-issued unk_ id. Required only for resolve."},
				"expected_revision": map[string]any{"type": "integer", "minimum": 1, "description": "Optimistic-concurrency revision required for resolve."},
				"question":          map[string]any{"type": "string", "description": "Unresolved question. Required only for open."},
				"blocking":          map[string]any{"type": "boolean", "description": "When true, the unknown must be resolved before final_review can pass."},
				"evidence_refs":     map[string]any{"type": "array", "maxItems": 16, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Supporting refs for open, or current-step-owned resolution refs for resolve."},
				"resolution":        map[string]any{"type": "string", "description": "Evidence-backed resolution. Required only for resolve."},
			},
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
			"status":            map[string]any{"type": "string", "enum": []string{"pass", "failed"}},
			"evidence":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"evidence_bindings": reviewEvidenceBindingsSchema("Server-validated bindings for every passing-axis evidence claim."),
			"findings":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

func reviewEvidenceBindingsSchema(description string) map[string]any {
	return map[string]any{
		"type": "array", "maxItems": 64, "description": description,
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"claim", "step_id"},
			"properties": map[string]any{
				"claim":            map[string]any{"type": "string", "description": "Exact verified/evidence claim this binding proves."},
				"step_id":          map[string]any{"type": "string", "description": "Completed task step that owns the cited outcome evidence."},
				"requirement_refs": map[string]any{"type": "array", "maxItems": 12, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Requirement refs owned by this step and proven by this claim."},
				"evidence_refs":    map[string]any{"type": "array", "maxItems": 16, "uniqueItems": true, "items": map[string]any{"type": "string"}, "description": "Evidence refs already owned by this completed step, including exec:// receipts, diagnostic:// step evidence, artifact://, commit://, test://, or other persisted execution refs."},
			},
		},
	}
}

func portableAuditBundleInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": true,
		"required":    []string{"contract", "manifest", "task", "graph"},
		"description": "Self-contained provenance-audit-bundle/v1 snapshot for offline verification; this is read-only input and never writes evidence.",
		"properties": map[string]any{
			"contract":           map[string]any{"type": "string", "const": portableAuditBundleContract},
			"manifest":           map[string]any{"type": "object"},
			"task":               map[string]any{"type": "object"},
			"execution_receipts": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			"execution_claims":   map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			"graph":              map[string]any{"type": "object"},
		},
	}
}

func taskManageInputObject(props map[string]any) map[string]any {
	schema := toolcontract.InputObject(props, "action")
	schema["allOf"] = []any{
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "admit"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"admission"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "get_admission"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"admission_ref"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "create"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"title", "admission_ref"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"enum": []string{"get_improvement_view", "improvement_update"}}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"task_id"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "improvement_update"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"improvement_update"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "handoff"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"task_id", "summary"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "backtrace"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"provenance_query"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"enum": []string{"revalidate", "export_audit_bundle", "export_signed_audit_bundle"}}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"task_id"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "verify_audit_bundle"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"audit_bundle"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "verify_signed_audit_bundle"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"audit_bundle", "audit_signature", "audit_trust_registry", "expected_root_key_id"}},
		},
		map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"action": map[string]any{"const": "verify_signed_audit_bundle_online"}},
				"required":   []string{"action"},
			},
			"then": map[string]any{"required": []string{"audit_bundle", "audit_signature"}},
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
		"action":                  stringProp("Completed task action."),
		"admission_ref":           stringProp("Immutable standards-first admission reference."),
		"admission_digest":        stringProp("SHA-256 digest of the frozen admission contract."),
		"admission":               objectProp("Frozen admission contract returned by admit or get_admission."),
		"workflow_match":          objectProp("Persisted workflow-match receipt when available."),
		"workflow_match_digest":   stringProp("SHA-256 digest of the workflow-match receipt bound to the admission."),
		"task_id":                 stringProp("Persistent task id returned by create and usable with task lifecycle actions."),
		"task":                    objectProp("Full persistent task state returned only by get."),
		"task_summary":            objectProp("Compact task summary returned by task lifecycle actions."),
		"improvement_view":        objectProp("Blind evaluator projection containing only anonymous aliases, separately staged identity-neutral artifact refs, probe summaries, evidence counts, and the frozen probe criteria. Author/provenance refs are withheld."),
		"improvement_candidate":   objectProp("Author/prober-facing compact candidate identity returned after register_candidate. Never pass this object to a blind evaluator."),
		"review_status":           stringProp("Final review status when present: not_started, pass, or failed."),
		"final_review":            objectProp("Compact final review state with status and counts."),
		"tasks":                   arrayProp("Compact task summaries ordered by most recent update."),
		"count":                   intProp("Returned item count."),
		"state_dir":               stringProp("Local AgentDock task state directory."),
		"next_required_action":    stringProp("Concise guidance for checkpoint progress, handoff/resume, or final review."),
		"resume_context":          objectProp("Derived fresh-session resume packet returned by handoff and get when a current handoff exists."),
		"backtrace":               objectProp("Read-only provenance-backtrace/v1 graph returned only by action=backtrace."),
		"integrity":               objectProp("Read-only provenance-integrity/v1 report returned by revalidation, export, or offline verification."),
		"audit_bundle":            objectProp("Portable provenance-audit-bundle/v1 returned when unsigned or signed export passes integrity validation."),
		"audit_signature":         objectProp("Detached audit-detached-signature/v1 returned by export_signed_audit_bundle."),
		"audit_trust_registry":    objectProp("Public root-signed audit-trust-registry/v1 snapshot returned by export_signed_audit_bundle."),
		"signed_verification":     objectProp("provenance-trust/v1 report returned by verify_signed_audit_bundle."),
		"freshness_verification":  objectProp("provenance-trust-freshness/v1 report returned when a root-signed trust checkpoint supplies freshness."),
		"trust_state":             objectProp("Current audit-trust-state/v1 verifier state returned by state-anchored online freshness verification."),
		"root_key_id":             stringProp("Pinned audit root key identity required to authenticate signed portable audit bundles."),
		"trust_registry_revision": intProp("Current root-signed trust registry revision. Pin the latest known revision with root_key_id to prevent stale-registry replay."),
		"bundle_sha256":           stringProp("Portable audit bundle SHA-256. P8 callers may pin this directly; P9 callers normally pin root_key_id plus trust_registry_revision instead."),
	}
	if cfg.NexusEndpoint != "" {
		props["evolution_handoff"] = objectProp("Candidate-only handoff derived from a composed reusable lesson. It does not mutate Evolution; use evolve propose separately.")
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
