package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

const runtimeAPISource = "agentdock-api"

func (r *Runtime) SetTrustCheckpointProvider(provider tooltask.TrustCheckpointProvider) {
	if r != nil && r.taskTools != nil {
		r.taskTools.WithTrustCheckpointProvider(provider)
	}
}

func (r *Runtime) RuntimeStatus() Result {
	tools := r.ToolNames()
	return Result{
		"ok":                    true,
		"source":                runtimeAPISource,
		"service":               config.ServerName,
		"version":               buildinfo.Version,
		"agentdock_home":        r.cfg.AgentDockHome,
		"agentdock_default_dir": r.cfg.AgentDockDefaultDir,
		"path_model":            config.PathModel,
		"auth_enabled":          r.cfg.AuthRequired(),
		"browser_enabled":       r.cfg.BrowserEnabled,
		"memory_enabled":        r.cfg.NexusEndpoint != "",
		"nexus_enabled":         strings.TrimSpace(r.cfg.NexusEndpoint) != "",
		"tool_count":            len(tools),
		"tools":                 tools,
	}
}

func (r *Runtime) RuntimeOverview() (Result, error) {
	tasksResult, err := r.RuntimeTasks("", 200)
	if err != nil {
		return nil, err
	}
	tasks, _ := tasksResult["tasks"].([]map[string]any)
	counts := map[string]int{
		"active": 0, "completed": 0, "blocked": 0, "active_recent_24h": 0,
	}
	recentCutoff := time.Now().UTC().Add(-24 * time.Hour)
	for _, task := range tasks {
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(task["status"])))
		switch status {
		case "active":
			counts["active"]++
			updatedAt := strings.TrimSpace(fmt.Sprint(task["updated_at"]))
			if parsed, parseErr := time.Parse(time.RFC3339Nano, updatedAt); parseErr == nil && !parsed.Before(recentCutoff) {
				counts["active_recent_24h"]++
			}
		case "completed":
			counts["completed"]++
		case "blocked":
			counts["blocked"]++
		}
	}

	skills, err := r.skillCapabilityIndex()
	if err != nil {
		return nil, err
	}
	return Result{
		"ok":     true,
		"source": runtimeAPISource,
		"tasks": Result{
			"active": counts["active"], "completed": counts["completed"], "blocked": counts["blocked"],
			"active_recent_24h": counts["active_recent_24h"],
		},
		"skills":  Result{"count": len(skills)},
		"plugins": Result{"count": 0, "available": false},
		"mcp":     Result{"count": len(r.dynamicMCP.CapabilityItems())},
	}, nil
}

func (r *Runtime) RuntimeSkills() (Result, error) {
	return r.skills.RuntimeSkills()
}

func (r *Runtime) RuntimeSkill(skill string) (Result, error) {
	return r.skills.RuntimeSkill(skill)
}

func (r *Runtime) RuntimeSkillFiles(skill string) (Result, error) {
	return r.skills.RuntimeSkillFiles(skill)
}

func (r *Runtime) RuntimeSkillFile(skill, relativePath string) (Result, error) {
	return r.skills.RuntimeSkillFile(skill, relativePath)
}

func (r *Runtime) RuntimeTasks(status string, limit int) (Result, error) {
	return r.taskTools.RuntimeTasks(status, limit)
}

func (r *Runtime) RuntimeTask(id string) (Result, error) {
	return r.taskTools.RuntimeTask(id)
}

func (r *Runtime) RuntimeTaskDelete(id string) (Result, error) {
	return r.taskTools.RuntimeTaskDelete(id)
}

func (r *Runtime) RuntimeCapabilities(ctx context.Context, refresh bool) (Result, error) {
	result, err := r.AgentDockContext(ctx)
	if err != nil {
		return nil, err
	}
	result["source"] = runtimeAPISource
	return result, nil
}

func (r *Runtime) RuntimeMCPServers(ctx context.Context) (Result, error) {
	return r.runtimeMCPManage(ctx, map[string]any{"action": "list"})
}

func (r *Runtime) RuntimeMCPServer(ctx context.Context, name string) (Result, error) {
	return r.runtimeMCPManage(ctx, map[string]any{"action": "inspect", "name": name})
}

func (r *Runtime) RuntimeMCPManage(ctx context.Context, args map[string]any) (Result, error) {
	return r.runtimeMCPManage(ctx, args)
}

func (r *Runtime) runtimeMCPManage(ctx context.Context, args map[string]any) (Result, error) {
	if err := r.validateToolArguments(toolmcp.ToolManage, args); err != nil {
		return nil, err
	}
	var request toolmcp.ManageRequest
	if err := decodeToolInput("mcp_manage", args, &request); err != nil {
		return nil, err
	}
	result, err := r.dynamicMCP.Manage(ctx, request)
	if err != nil {
		return nil, err
	}
	result["ok"] = true
	result["source"] = runtimeAPISource
	return result, nil
}
