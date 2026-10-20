package skill

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolPackage   = "skill_package"
	ToolDiscovery = "skill_discovery"
	ToolEphemeral = "skill_ephemeral"
)

func PackageInputSchema() map[string]any {
	stringProp := toolcontract.String
	return toolcontract.InputObject(map[string]any{
		"action":                    map[string]any{"type": "string", "description": "Skill package or isolated environment action.", "enum": []string{"validate", "install", "uninstall", "activate", "rollback", "bind_provenance", "env_set", "env_unset", "env_list"}},
		"skill":                     stringProp("Skill name for uninstall, activate, rollback, or environment management."),
		"version":                   stringProp("Installed Skill version for uninstall or activate. Omit for uninstall to remove the whole Skill."),
		"key":                       stringProp("Environment variable name for env_set/env_unset."),
		"value":                     stringProp("Environment variable value for env_set. Secret values are never returned."),
		"source":                    stringProp("Host path or HTTP(S) URL for validate/install."),
		"digest":                    stringProp("Optional expected SHA-256 digest for validate/install."),
		"activate":                  toolcontract.Boolean("Activate the installed version. Defaults to true."),
		"max_bytes":                 toolcontract.Integer("Maximum validate/install package bytes."),
		"plan_sha256":               stringProp("Validated adoption-plan SHA-256 for bind_provenance."),
		"activation_receipt_sha256": stringProp("Terminal activate-version receipt SHA-256 for bind_provenance."),
		"artifact_sha256":           stringProp("Receipt-chain package artifact SHA-256 for bind_provenance."),
		"provenance_bundle":         map[string]any{"type": "object", "description": "Validated provenance bundle containing plan and receipt chain."},
	}, "action")
}

func PackageOutputSchema() map[string]any {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	objectProp := toolcontract.OpenObject
	return toolcontract.OutputObject(map[string]any{
		"action":     stringProp("Completed Skill package action."),
		"skill":      stringProp("Skill name."),
		"name":       stringProp("Skill name for environment actions."),
		"key":        stringProp("Environment variable name. Secret values are never returned."),
		"configured": boolProp("Whether the environment variable has a non-empty configured value."),
		"removed":    boolProp("Whether the environment variable was removed."),
		"items":      arrayProp("Environment variable names and configured status without values."),
		"count":      intProp("Returned environment variable count."),
		"valid":      boolProp("Whether a Skill source passed validation."),
		"source":     stringProp("Resolved Skill source label."),
		"digest":     stringProp("Computed Skill package digest."),
		"issues":     arrayProp("Structured validation issues."),
		"document":   objectProp("Parsed SKILL.md frontmatter and body metadata."),
		"result":     objectProp("Install, uninstall, activate, or rollback result."),
	})
}

func DiscoveryInputSchema() map[string]any {
	stringProp := toolcontract.String
	return toolcontract.InputObject(map[string]any{
		"action": map[string]any{"type": "string", "description": "Read-only external Skill discovery action.", "enum": []string{"search", "pin"}},
		"query":  stringProp("Search query for skills.sh. Required for search."),
		"owner":  stringProp("Optional GitHub owner filter for skills.sh search."),
		"limit":  toolcontract.BoundedInteger("Maximum candidates to return. Defaults to 10 and is capped at 20.", 1, 20),
		"source": stringProp("Public github.com owner/repo or repository URL to resolve to an immutable HEAD commit. Required for pin."),
		"skill":  stringProp("Optional selected Skill name recorded in the immutable source lock."),
	}, "action")
}

func DiscoveryOutputSchema() map[string]any {
	stringProp := toolcontract.String
	return toolcontract.OutputObject(map[string]any{
		"action":             stringProp("Completed discovery action."),
		"query":              stringProp("Search query."),
		"owner":              stringProp("Optional GitHub owner filter."),
		"count":              toolcontract.Integer("Returned candidate count."),
		"candidates":         toolcontract.ObjectArray("Read-only external Skill candidates. Each candidate has install_authorized=false."),
		"lock":               toolcontract.OpenObject("Immutable source lock suitable as evidence for project-due-diligence."),
		"lock_sha256":        stringProp("Deterministic SHA-256 of the immutable source lock."),
		"resolved_at":        stringProp("UTC timestamp when the repository HEAD was resolved."),
		"review_required":    toolcontract.Boolean("Whether project-level due diligence is required before downstream execution."),
		"install_authorized": toolcontract.Boolean("Always false. Discovery and pinning never authorize installation."),
		"next_owner":         stringProp("Required next governance owner for a pinned candidate."),
	})
}

func EphemeralInputSchema() map[string]any {
	stringProp := toolcontract.String
	return toolcontract.InputObject(map[string]any{
		"action":               map[string]any{"type": "string", "description": "Transient external Skill binding action.", "enum": []string{"bind", "list", "clear"}},
		"skill":                stringProp("Exact Skill name to bind. Required for bind and must be an exact approved_unit in the adoption plan."),
		"binding_id":           stringProp("Runtime-local ephemeral binding id. Required for clear."),
		"source_lock":          toolcontract.OpenObject("Validated agentdock/skill-source-lock/v1 object from skill_discovery pin."),
		"source_lock_sha256":   stringProp("Deterministic SHA-256 of source_lock."),
		"adoption_plan":        toolcontract.OpenObject("project-due-diligence/adoption-plan/v1 object authorizing exact ADOPT use."),
		"adoption_plan_sha256": stringProp("Canonical JSON SHA-256 of adoption_plan."),
	}, "action")
}

func EphemeralOutputSchema() map[string]any {
	return toolcontract.OutputObject(map[string]any{
		"action":             toolcontract.String("Completed ephemeral binding action."),
		"binding":            toolcontract.OpenObject("One active runtime-local ephemeral Skill binding."),
		"bindings":           toolcontract.ObjectArray("Current runtime-local ephemeral Skill bindings."),
		"count":              toolcontract.Integer("Current binding count."),
		"binding_id":         toolcontract.String("Runtime-local binding id."),
		"removed":            toolcontract.Boolean("Whether a binding was removed."),
		"persistent":         toolcontract.Boolean("Always false. Ephemeral bindings do not persist across Runtime shutdown."),
		"install_authorized": toolcontract.Boolean("Always false. Ephemeral binding never grants package installation authority."),
	})
}

func InputSchema(name string) (map[string]any, bool) {
	switch name {
	case ToolPackage:
		return PackageInputSchema(), true
	case ToolDiscovery:
		return DiscoveryInputSchema(), true
	case ToolEphemeral:
		return EphemeralInputSchema(), true
	default:
		return nil, false
	}
}

func OutputSchema(name string) (map[string]any, bool) {
	switch name {
	case ToolPackage:
		return PackageOutputSchema(), true
	case ToolDiscovery:
		return DiscoveryOutputSchema(), true
	case ToolEphemeral:
		return EphemeralOutputSchema(), true
	default:
		return nil, false
	}
}
