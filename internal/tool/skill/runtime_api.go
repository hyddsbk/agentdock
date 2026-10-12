package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	skills "github.com/uvwt/agentdock/internal/skill"
)

type CapabilityItem struct {
	Name        string
	Description string
	File        string
	Bundled     bool
	SkillRef    string
	SourceType  string
}

func managedSkillRef(skill string) string {
	return "skill://managed/" + strings.TrimSpace(skill)
}

func runtimeSkillContentDigest(packageDir string, files []runtimeSkillFile) (string, error) {
	metadata, err := os.ReadFile(filepath.Join(packageDir, ".agentdock-install.json"))
	if err == nil {
		var recorded struct {
			ContentDigest string `json:"content_digest"`
		}
		if json.Unmarshal(metadata, &recorded) == nil {
			value := strings.ToLower(strings.TrimSpace(recorded.ContentDigest))
			if strings.HasPrefix(value, "sha256:") {
				raw := strings.TrimPrefix(value, "sha256:")
				if len(raw) == 64 {
					if _, decodeErr := hex.DecodeString(raw); decodeErr == nil {
						return "sha256:" + raw, nil
					}
				}
			}
		}
	}

	// Legacy or directly provisioned packages may not have install metadata.
	// Hash only the regular files that Runtime already considers safe to expose;
	// symbolic links and private AgentDock metadata stay outside the identity.
	h := sha256.New()
	for _, file := range files {
		data, readErr := os.ReadFile(filepath.Join(packageDir, filepath.FromSlash(file.Path)))
		if readErr != nil {
			return "", readErr
		}
		h.Write([]byte(file.Path))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) CapabilityItems() ([]CapabilityItem, error) {
	names, err := s.state.ListSkills()
	if err != nil {
		return nil, err
	}
	bundledNames, err := s.state.BundledSkills()
	if err != nil {
		return nil, err
	}
	bundled := make(map[string]struct{}, len(bundledNames))
	for _, name := range bundledNames {
		bundled[name] = struct{}{}
	}
	items := make([]CapabilityItem, 0, len(names))
	for _, name := range names {
		packageDir, resolveErr := s.state.Resolve(name, "")
		if resolveErr != nil || skills.ValidatePackage(packageDir) != nil {
			continue
		}
		doc, loadErr := skills.LoadSkillDocument(packageDir)
		if loadErr != nil {
			continue
		}
		_, isBundled := bundled[name]
		items = append(items, CapabilityItem{
			Name: name, Description: strings.TrimSpace(doc.Description), File: "skill://" + name + "/SKILL.md", Bundled: isBundled,
			SkillRef: managedSkillRef(name), SourceType: "managed",
		})
	}
	return items, nil
}

func (s *Service) RuntimeSkills() (Result, error) {
	result, err := s.list()
	if err != nil {
		return nil, err
	}
	items, _ := result["skills"].([]map[string]any)
	current := make([]map[string]any, 0, len(items))
	for _, item := range items {
		skill, _ := item["skill"].(string)
		version, _ := item["active_version"].(string)
		if strings.TrimSpace(skill) == "" || strings.TrimSpace(version) == "" {
			continue
		}
		packageDir, err := s.state.InstalledPath(skill, version)
		if err != nil {
			return nil, skillToolError(err)
		}
		document, err := skills.LoadSkillDocument(packageDir)
		if err != nil {
			return nil, skillToolError(err)
		}
		files, err := collectRuntimeSkillFiles(packageDir)
		if err != nil {
			return nil, err
		}
		contentDigest, err := runtimeSkillContentDigest(packageDir, files)
		if err != nil {
			return nil, skillToolError(err)
		}
		item["name"] = document.Name
		item["description"] = document.Description
		item["file_count"] = len(files)
		item["skill_ref"] = managedSkillRef(skill)
		item["source_type"] = "managed"
		item["content_digest"] = contentDigest
		current = append(current, item)
	}
	result["skills"] = current
	result["count"] = len(current)
	result["source"] = runtimeAPISource
	return result, nil
}

func (s *Service) RuntimeSkill(skill string) (Result, error) {
	result, err := s.inspect(InspectRequest{Skill: skill})
	if err != nil {
		return nil, err
	}
	result["source"] = runtimeAPISource
	result["files"] = []runtimeSkillFile{}
	result["file_count"] = 0
	version, _ := result["version"].(string)
	if strings.TrimSpace(version) == "" {
		return nil, toolErrorDetails("SKILL_NOT_ACTIVE", "skill has no active version", "not_found", map[string]any{"skill": skill})
	}
	packageDir, err := s.state.InstalledPath(skill, version)
	if err != nil {
		return nil, skillToolError(err)
	}
	document, err := skills.LoadSkillDocument(packageDir)
	if err != nil {
		return nil, skillToolError(err)
	}
	files, err := collectRuntimeSkillFiles(packageDir)
	if err != nil {
		return nil, err
	}
	contentDigest, err := runtimeSkillContentDigest(packageDir, files)
	if err != nil {
		return nil, skillToolError(err)
	}
	result["name"] = document.Name
	result["description"] = document.Description
	result["skill_ref"] = managedSkillRef(skill)
	result["source_type"] = "managed"
	result["content_digest"] = contentDigest
	result["files"] = files
	result["file_count"] = len(files)
	return result, nil
}
