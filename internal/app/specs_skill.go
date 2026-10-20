package app

import (
	"context"

	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
)

func skillToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "skill_package", Contract: skillToolContract, Title: "Manage Skill packages", Description: "Validate, install, uninstall, activate, or roll back AgentDock Skill packages and manage each Skill's isolated environment without returning secret values.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("skill_package", func(ctx context.Context, r *Runtime, request toolskill.PackageRequest) (Result, error) {
			return r.skills.Package(ctx, request)
		})},
		{Name: "skill_discovery", Contract: skillToolContract, Title: "Discover external Skills", Description: "Read-only external Skill discovery. Search bounded skills.sh candidates or pin one public GitHub repository to an immutable commit for project-due-diligence. This tool never installs, updates, activates, removes, or authorizes a Skill.", Annotations: readOnlyToolAnnotations(true), Handler: typedToolHandler("skill_discovery", func(ctx context.Context, r *Runtime, request toolskill.DiscoveryRequest) (Result, error) {
			return r.skills.Discovery(ctx, request)
		})},
		{Name: "skill_ephemeral", Contract: skillToolContract, Title: "Bind ephemeral Skill", Description: "Temporarily bind one immutable, project-due-diligence-approved external Skill to the current AgentDock runtime. Binding materializes reviewed files only in runtime temp storage, never installs or activates a package, and is removed on clear or Runtime shutdown.", Annotations: mutatingToolAnnotations(false, true), Handler: typedToolHandler("skill_ephemeral", func(ctx context.Context, r *Runtime, request toolskill.EphemeralRequest) (Result, error) {
			return r.skills.Ephemeral(ctx, request)
		})},
	}
}
