package interop

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type RootBase string

const (
	RootProject    RootBase = "project"
	RootHome       RootBase = "home"
	RootConfigHome RootBase = "config_home"
	RootEnv        RootBase = "env"
)

type RootSpec struct {
	Base     RootBase `json:"base"`
	EnvVar   string   `json:"env_var,omitempty"`
	Path     string   `json:"path"`
	Optional bool     `json:"optional,omitempty"`
}

type AgentAdapter struct {
	ID                string     `json:"id"`
	DisplayName       string     `json:"display_name"`
	ProjectSkillRoots []RootSpec `json:"project_skill_roots"`
	GlobalSkillRoots  []RootSpec `json:"global_skill_roots,omitempty"`
}

type PathContext struct {
	ProjectDir string
	HomeDir    string
	ConfigHome string
	Env        map[string]string
}

var builtinAdapters = map[string]AgentAdapter{
	"universal": {
		ID: "universal", DisplayName: "Universal Agent Skills",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".agents/skills"}},
		GlobalSkillRoots:  []RootSpec{{Base: RootHome, Path: ".agents/skills"}},
	},
	"codex": {
		ID: "codex", DisplayName: "Codex",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".agents/skills"}},
		GlobalSkillRoots:  []RootSpec{{Base: RootHome, Path: ".agents/skills"}},
	},
	"claude-code": {
		ID: "claude-code", DisplayName: "Claude Code",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".claude/skills"}},
		GlobalSkillRoots: []RootSpec{
			{Base: RootEnv, EnvVar: "CLAUDE_CONFIG_DIR", Path: "skills", Optional: true},
			{Base: RootHome, Path: ".claude/skills"},
		},
	},
	"cursor": {
		ID: "cursor", DisplayName: "Cursor",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".agents/skills"}},
		GlobalSkillRoots:  []RootSpec{{Base: RootHome, Path: ".cursor/skills"}},
	},
	"opencode": {
		ID: "opencode", DisplayName: "OpenCode",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".agents/skills"}},
		GlobalSkillRoots:  []RootSpec{{Base: RootConfigHome, Path: "opencode/skills"}},
	},
	"gemini-cli": {
		ID: "gemini-cli", DisplayName: "Gemini CLI",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".agents/skills"}},
		GlobalSkillRoots:  []RootSpec{{Base: RootHome, Path: ".gemini/skills"}},
	},
	"github-copilot": {
		ID: "github-copilot", DisplayName: "GitHub Copilot",
		ProjectSkillRoots: []RootSpec{{Base: RootProject, Path: ".agents/skills"}},
		GlobalSkillRoots:  []RootSpec{{Base: RootHome, Path: ".copilot/skills"}},
	},
}

func Adapter(id string) (AgentAdapter, bool) {
	item, ok := builtinAdapters[strings.ToLower(strings.TrimSpace(id))]
	return cloneAdapter(item), ok
}

func AdapterIDs() []string {
	ids := make([]string, 0, len(builtinAdapters))
	for id := range builtinAdapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func ResolveRoot(spec RootSpec, ctx PathContext) (string, bool, error) {
	if err := validateRootSpec(spec); err != nil {
		return "", false, err
	}
	var base string
	switch spec.Base {
	case RootProject:
		base = strings.TrimSpace(ctx.ProjectDir)
	case RootHome:
		base = strings.TrimSpace(ctx.HomeDir)
	case RootConfigHome:
		base = strings.TrimSpace(ctx.ConfigHome)
	case RootEnv:
		base = strings.TrimSpace(ctx.Env[spec.EnvVar])
	default:
		return "", false, fmt.Errorf("unsupported root base %q", spec.Base)
	}
	if base == "" {
		if spec.Optional {
			return "", false, nil
		}
		return "", false, fmt.Errorf("base %q is unavailable for required skill root", spec.Base)
	}
	return filepath.Clean(filepath.Join(base, filepath.FromSlash(spec.Path))), true, nil
}

func ResolveRoots(specs []RootSpec, ctx PathContext) ([]string, error) {
	out := make([]string, 0, len(specs))
	seen := map[string]struct{}{}
	for _, spec := range specs {
		resolved, ok, err := ResolveRoot(spec, ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		key := filepath.Clean(resolved)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, resolved)
	}
	return out, nil
}

func validateRootSpec(spec RootSpec) error {
	if strings.TrimSpace(spec.Path) == "" {
		return errors.New("skill root path is required")
	}
	clean := filepath.Clean(filepath.FromSlash(spec.Path))
	if filepath.IsAbs(clean) {
		return errors.New("skill root path must be relative to its declared base")
	}
	if clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
		return errors.New("skill root path must stay within its declared base")
	}
	if spec.Base == RootEnv && strings.TrimSpace(spec.EnvVar) == "" {
		return errors.New("env root requires env_var")
	}
	if spec.Base != RootEnv && strings.TrimSpace(spec.EnvVar) != "" {
		return errors.New("env_var is only valid for env roots")
	}
	return nil
}

func cloneAdapter(in AgentAdapter) AgentAdapter {
	in.ProjectSkillRoots = append([]RootSpec(nil), in.ProjectSkillRoots...)
	in.GlobalSkillRoots = append([]RootSpec(nil), in.GlobalSkillRoots...)
	return in
}
