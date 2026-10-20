package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	skills "github.com/uvwt/agentdock/internal/skill"
	skillinterop "github.com/uvwt/agentdock/internal/skill/interop"
)

const (
	maxEphemeralArchiveBytes   int64 = 16 << 20
	maxEphemeralExtractedBytes int64 = 32 << 20
	maxEphemeralFileBytes      int64 = 4 << 20
	maxEphemeralFiles                = 512
)

type snapshotFetcher interface {
	Fetch(context.Context, skillinterop.SourceRef) (tempRoot string, repositoryRoot string, err error)
}

type githubSnapshotFetcher struct {
	client  *http.Client
	baseURL string
}

type ephemeralBinding struct {
	ID                 string
	Skill              string
	Description        string
	TempRoot           string
	PackageDir         string
	File               string
	SkillRef           string
	Source             string
	ContentDigest      string
	SourceLockSHA256   string
	AdoptionPlanSHA256 string
	BoundAt            time.Time
}

type EphemeralBindingInfo struct {
	ID                 string    `json:"id"`
	Skill              string    `json:"skill"`
	Description        string    `json:"description"`
	File               string    `json:"file"`
	SkillRef           string    `json:"skill_ref"`
	SourceType         string    `json:"source_type"`
	Source             string    `json:"source"`
	ContentDigest      string    `json:"content_digest"`
	SourceLockSHA256   string    `json:"source_lock_sha256"`
	AdoptionPlanSHA256 string    `json:"adoption_plan_sha256"`
	BoundAt            time.Time `json:"bound_at"`
}

type ephemeralManager struct {
	mu       sync.RWMutex
	fetcher  snapshotFetcher
	bindings map[string]ephemeralBinding
	bySkill  map[string]string
}

func newEphemeralManager() *ephemeralManager {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("ephemeral snapshot redirects are not allowed")
		},
	}
	return &ephemeralManager{
		fetcher:  &githubSnapshotFetcher{client: client, baseURL: "https://codeload.github.com"},
		bindings: map[string]ephemeralBinding{},
		bySkill:  map[string]string{},
	}
}

func (s *Service) Ephemeral(ctx context.Context, request EphemeralRequest) (Result, error) {
	switch strings.TrimSpace(request.Action) {
	case "bind":
		binding, err := s.ephemeral.bind(ctx, func(name string) (bool, error) {
			version, err := s.state.ActiveVersion(name)
			if err != nil {
				return false, err
			}
			return strings.TrimSpace(version) != "", nil
		}, request)
		if err != nil {
			return nil, err
		}
		return Result{
			"action":             "bind",
			"binding":            binding,
			"install_authorized": false,
			"persistent":         false,
		}, nil
	case "list":
		items := s.ephemeral.list()
		return Result{
			"action":     "list",
			"bindings":   items,
			"count":      len(items),
			"persistent": false,
		}, nil
	case "clear":
		removed, err := s.ephemeral.clear(request.BindingID)
		if err != nil {
			return nil, toolErrorDetails("EPHEMERAL_CLEAR_FAILED", "clear ephemeral Skill binding", "runtime", map[string]any{"binding_id": request.BindingID, "reason": err.Error()})
		}
		return Result{
			"action":     "clear",
			"binding_id": strings.TrimSpace(request.BindingID),
			"removed":    removed,
			"persistent": false,
		}, nil
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported skill_ephemeral action", "validation", map[string]any{
			"action":  request.Action,
			"allowed": []string{"bind", "list", "clear"},
		})
	}
}

func (m *ephemeralManager) bind(ctx context.Context, managedActive func(string) (bool, error), request EphemeralRequest) (EphemeralBindingInfo, error) {
	planRaw, err := marshalOpenObject(request.AdoptionPlan, "adoption_plan")
	if err != nil {
		return EphemeralBindingInfo{}, err
	}
	authorization, err := skillinterop.ValidateEphemeralAuthorization(
		request.Skill,
		request.SourceLock,
		request.SourceLockSHA256,
		planRaw,
		request.AdoptionPlanSHA256,
	)
	if err != nil {
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_AUTHORIZATION_FAILED", "validate ephemeral Skill authorization", "validation", map[string]any{"reason": err.Error()})
	}

	active, err := managedActive(authorization.Skill)
	if err != nil {
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_STATE_CHECK_FAILED", "check managed Skill ownership", "runtime", map[string]any{"skill": authorization.Skill, "reason": err.Error()})
	}
	if active {
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_SKILL_CONFLICT", "an active managed Skill already owns this name", "conflict", map[string]any{"skill": authorization.Skill})
	}

	m.mu.RLock()
	existingID := m.bySkill[authorization.Skill]
	if existingID != "" {
		existing := m.bindings[existingID]
		m.mu.RUnlock()
		if existing.SourceLockSHA256 == authorization.SourceLockSHA256 && existing.AdoptionPlanSHA256 == authorization.AdoptionPlanSHA256 {
			return publicEphemeralBinding(existing), nil
		}
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_SKILL_CONFLICT", "a different ephemeral binding already owns this Skill name", "conflict", map[string]any{"skill": authorization.Skill, "binding_id": existingID})
	}
	m.mu.RUnlock()

	tempRoot, repoRoot, err := m.fetcher.Fetch(ctx, authorization.SourceLock.Source)
	if err != nil {
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_FETCH_FAILED", "fetch pinned Skill snapshot", "network", map[string]any{"reason": err.Error()})
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempRoot)
		}
	}()

	packageDir, metadata, err := selectEphemeralSkill(repoRoot, authorization.Skill)
	if err != nil {
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_SKILL_INVALID", "select reviewed Skill from pinned snapshot", "validation", map[string]any{"reason": err.Error()})
	}
	if err := skills.ValidatePackage(packageDir); err != nil {
		return EphemeralBindingInfo{}, skillToolError(err)
	}
	digest, err := skills.DigestDirectory(packageDir)
	if err != nil {
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_DIGEST_FAILED", "digest ephemeral Skill content", "runtime", map[string]any{"reason": err.Error()})
	}

	bindingID := ephemeralBindingID(authorization.Skill, authorization.SourceLock.Source.Commit)
	binding := ephemeralBinding{
		ID:                 bindingID,
		Skill:              authorization.Skill,
		Description:        strings.TrimSpace(metadata.Description),
		TempRoot:           tempRoot,
		PackageDir:         packageDir,
		File:               "skill://" + bindingID + "/SKILL.md",
		SkillRef:           "skill://ephemeral/" + bindingID,
		Source:             authorization.SourceLock.Source.Canonical,
		ContentDigest:      digest,
		SourceLockSHA256:   authorization.SourceLockSHA256,
		AdoptionPlanSHA256: authorization.AdoptionPlanSHA256,
		BoundAt:            time.Now().UTC(),
	}

	m.mu.Lock()
	if currentID := m.bySkill[binding.Skill]; currentID != "" {
		current := m.bindings[currentID]
		m.mu.Unlock()
		if current.SourceLockSHA256 == binding.SourceLockSHA256 && current.AdoptionPlanSHA256 == binding.AdoptionPlanSHA256 {
			return publicEphemeralBinding(current), nil
		}
		return EphemeralBindingInfo{}, toolErrorDetails("EPHEMERAL_SKILL_CONFLICT", "a concurrent ephemeral binding already owns this Skill name", "conflict", map[string]any{"skill": binding.Skill, "binding_id": currentID})
	}
	m.bindings[binding.ID] = binding
	m.bySkill[binding.Skill] = binding.ID
	m.mu.Unlock()
	cleanup = false
	return publicEphemeralBinding(binding), nil
}

func (m *ephemeralManager) list() []EphemeralBindingInfo {
	m.mu.RLock()
	items := make([]EphemeralBindingInfo, 0, len(m.bindings))
	for _, binding := range m.bindings {
		items = append(items, publicEphemeralBinding(binding))
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Skill != items[j].Skill {
			return items[i].Skill < items[j].Skill
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func (m *ephemeralManager) clear(bindingID string) (bool, error) {
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return false, errors.New("binding_id is required")
	}
	m.mu.Lock()
	binding, ok := m.bindings[bindingID]
	if ok {
		delete(m.bindings, bindingID)
		delete(m.bySkill, binding.Skill)
	}
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	if err := os.RemoveAll(binding.TempRoot); err != nil {
		return true, err
	}
	return true, nil
}

func (m *ephemeralManager) close() error {
	m.mu.Lock()
	roots := make([]string, 0, len(m.bindings))
	for _, binding := range m.bindings {
		roots = append(roots, binding.TempRoot)
	}
	m.bindings = map[string]ephemeralBinding{}
	m.bySkill = map[string]string{}
	m.mu.Unlock()

	var errs []error
	for _, root := range roots {
		if err := os.RemoveAll(root); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *ephemeralManager) capabilityItems() []CapabilityItem {
	m.mu.RLock()
	items := make([]CapabilityItem, 0, len(m.bindings))
	for _, binding := range m.bindings {
		items = append(items, CapabilityItem{
			Name:        binding.Skill,
			Description: binding.Description,
			File:        binding.File,
			SkillRef:    binding.SkillRef,
			SourceType:  "ephemeral",
		})
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (m *ephemeralManager) resolve(bindingID, resourcePath string) (string, string, error) {
	m.mu.RLock()
	binding, ok := m.bindings[bindingID]
	m.mu.RUnlock()
	if !ok {
		return "", "", toolErrorDetails("EPHEMERAL_BINDING_NOT_FOUND", "ephemeral Skill binding is not active", "not_found", map[string]any{"binding_id": bindingID})
	}
	realRoot, err := filepath.EvalSymlinks(binding.PackageDir)
	if err != nil {
		return "", "", toolErrorDetails("EPHEMERAL_PATH_INVALID", "cannot resolve ephemeral Skill root", "runtime", map[string]any{"binding_id": bindingID})
	}
	candidate := filepath.Join(realRoot, filepath.FromSlash(resourcePath))
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", toolErrorDetails("EPHEMERAL_RESOURCE_NOT_FOUND", "ephemeral Skill resource does not exist", "not_found", map[string]any{"binding_id": bindingID, "path": resourcePath})
		}
		return "", "", toolErrorDetails("EPHEMERAL_PATH_INVALID", "cannot resolve ephemeral Skill resource", "runtime", map[string]any{"binding_id": bindingID, "path": resourcePath})
	}
	rel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", "", toolErrorDetails("EPHEMERAL_PATH_ESCAPE", "ephemeral Skill resource escapes its binding root", "validation", map[string]any{"binding_id": bindingID, "path": resourcePath})
	}
	return realCandidate, "skill://" + bindingID + "/" + path.Clean(resourcePath), nil
}

func publicEphemeralBinding(binding ephemeralBinding) EphemeralBindingInfo {
	return EphemeralBindingInfo{
		ID:                 binding.ID,
		Skill:              binding.Skill,
		Description:        binding.Description,
		File:               binding.File,
		SkillRef:           binding.SkillRef,
		SourceType:         "ephemeral",
		Source:             binding.Source,
		ContentDigest:      binding.ContentDigest,
		SourceLockSHA256:   binding.SourceLockSHA256,
		AdoptionPlanSHA256: binding.AdoptionPlanSHA256,
		BoundAt:            binding.BoundAt,
	}
}

func ephemeralBindingID(skill, commit string) string {
	return "ephemeral--" + skill + "--" + commit[:12]
}

func selectEphemeralSkill(repositoryRoot, skillName string) (string, skills.SkillMetadata, error) {
	type match struct {
		dir      string
		metadata skills.SkillMetadata
	}
	matches := make([]match, 0, 1)
	err := filepath.WalkDir(repositoryRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("snapshot contains a symbolic link")
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxEphemeralFileBytes {
			return errors.New("SKILL.md exceeds ephemeral file limit")
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		metadata, err := skills.ParseSkillMetadata(data)
		if err != nil {
			return nil
		}
		if metadata.Name == skillName {
			matches = append(matches, match{dir: filepath.Dir(current), metadata: metadata})
		}
		return nil
	})
	if err != nil {
		return "", skills.SkillMetadata{}, err
	}
	if len(matches) == 0 {
		return "", skills.SkillMetadata{}, fmt.Errorf("no valid SKILL.md matched %q", skillName)
	}
	if len(matches) > 1 {
		return "", skills.SkillMetadata{}, fmt.Errorf("multiple SKILL.md files matched %q", skillName)
	}
	return matches[0].dir, matches[0].metadata, nil
}

func (f *githubSnapshotFetcher) Fetch(ctx context.Context, source skillinterop.SourceRef) (string, string, error) {
	if source.Kind != skillinterop.SourceGitHub || !source.Immutable || source.Owner == "" || source.Repo == "" || source.Commit == "" {
		return "", "", errors.New("snapshot source must be an immutable GitHub SourceRef")
	}
	base, err := url.Parse(f.baseURL)
	if err != nil {
		return "", "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + url.PathEscape(source.Owner) + "/" + url.PathEscape(source.Repo) + "/zip/" + source.Commit

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/zip")
	req.Header.Set("User-Agent", "AgentDock-Ephemeral-Skill/1")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("codeload returned HTTP %d", resp.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxEphemeralArchiveBytes+1))
	if err != nil {
		return "", "", err
	}
	if int64(len(archive)) > maxEphemeralArchiveBytes {
		return "", "", errors.New("ephemeral snapshot archive exceeds 16 MiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", "", err
	}
	return extractEphemeralZip(reader)
}

func extractEphemeralZip(reader *zip.Reader) (string, string, error) {
	tempRoot, err := os.MkdirTemp("", "agentdock-ephemeral-skill-*")
	if err != nil {
		return "", "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempRoot)
		}
	}()

	var top string
	var extracted int64
	files := 0
	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			return "", "", errors.New("snapshot contains an unsafe absolute path")
		}
		for _, segment := range strings.Split(name, "/") {
			if segment == ".." {
				return "", "", errors.New("snapshot contains path traversal")
			}
		}
		clean := path.Clean(strings.TrimPrefix(name, "./"))
		if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, "../") {
			return "", "", errors.New("snapshot contains path traversal")
		}
		segments := strings.Split(clean, "/")
		if len(segments) == 0 || segments[0] == "" {
			return "", "", errors.New("snapshot has no repository root")
		}
		if top == "" {
			top = segments[0]
		} else if top != segments[0] {
			return "", "", errors.New("snapshot contains multiple repository roots")
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return "", "", errors.New("snapshot contains a symbolic link")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(filepath.Join(tempRoot, filepath.FromSlash(clean)), 0o700); err != nil {
				return "", "", err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return "", "", errors.New("snapshot contains a non-regular file")
		}
		files++
		if files > maxEphemeralFiles {
			return "", "", fmt.Errorf("snapshot exceeds %d files", maxEphemeralFiles)
		}
		if entry.UncompressedSize64 > uint64(maxEphemeralFileBytes) {
			return "", "", fmt.Errorf("snapshot file %q exceeds 4 MiB", clean)
		}
		target := filepath.Join(tempRoot, filepath.FromSlash(clean))
		rel, err := filepath.Rel(tempRoot, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			return "", "", errors.New("snapshot path escapes temp root")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", "", err
		}
		src, err := entry.Open()
		if err != nil {
			return "", "", err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = src.Close()
			return "", "", err
		}
		written, copyErr := io.Copy(dst, io.LimitReader(src, maxEphemeralFileBytes+1))
		closeErr := errors.Join(src.Close(), dst.Close())
		if copyErr != nil {
			return "", "", copyErr
		}
		if closeErr != nil {
			return "", "", closeErr
		}
		if written > maxEphemeralFileBytes {
			return "", "", fmt.Errorf("snapshot file %q exceeds 4 MiB", clean)
		}
		extracted += written
		if extracted > maxEphemeralExtractedBytes {
			return "", "", errors.New("snapshot extracted content exceeds 32 MiB")
		}
	}
	if top == "" {
		return "", "", errors.New("snapshot archive is empty")
	}
	repoRoot := filepath.Join(tempRoot, filepath.FromSlash(top))
	info, err := os.Stat(repoRoot)
	if err != nil || !info.IsDir() {
		return "", "", errors.New("snapshot repository root is missing")
	}
	cleanup = false
	return tempRoot, repoRoot, nil
}

func marshalOpenObject(value map[string]any, field string) ([]byte, error) {
	if value == nil {
		return nil, toolErrorDetails("VALIDATION_ERROR", field+" is required", "validation", map[string]any{"field": field})
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, toolErrorDetails("VALIDATION_ERROR", field+" must be JSON serializable", "validation", map[string]any{"field": field})
	}
	return raw, nil
}
