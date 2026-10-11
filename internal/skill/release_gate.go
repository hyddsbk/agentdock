package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxReleaseGateTextFileBytes int64 = 4 << 20

type ReleaseGateViolation struct {
	Rule    string
	Path    string
	Line    int
	Message string
}

func (v ReleaseGateViolation) Error() string {
	location := v.Path
	if v.Line > 0 {
		location = fmt.Sprintf("%s:%d", v.Path, v.Line)
	}
	if location == "" {
		return v.Message
	}
	return fmt.Sprintf("%s: %s", location, v.Message)
}

func (v ReleaseGateViolation) packageError() error {
	return packageError(ErrReleaseGateFailed, "release_gate.seo."+v.Rule, errors.New(v.Error()))
}

type releaseGateTextFile struct {
	Path  string
	Lines []string
}

var releaseGateTextExtensions = map[string]struct{}{
	".md": {}, ".txt": {}, ".json": {}, ".yaml": {}, ".yml": {}, ".toml": {},
	".ini": {}, ".cfg": {}, ".conf": {}, ".sh": {}, ".bash": {}, ".zsh": {},
	".ps1": {}, ".bat": {}, ".cmd": {}, ".py": {}, ".js": {}, ".mjs": {},
	".cjs": {}, ".ts": {}, ".tsx": {}, ".jsx": {}, ".go": {}, ".rb": {},
	".php": {}, ".html": {}, ".htm": {}, ".css": {}, ".xml": {},
}

var (
	legacyRuntimePattern    = regexp.MustCompile("(?i)(?:claude[-_ ]seo\\b|(?:~[/\\\\])?\\.claude[/\\\\](?:skills|commands|agents)\\b|\\bclaude\\s+seo\\b)")
	windowsFixedPathPattern = regexp.MustCompile("(?i)\\b[a-z]:\\\\[^\\s'\"<>|]+")
	posixFixedPathPattern   = regexp.MustCompile("(?:^|[\\s'(])/(?:Users|home|opt|var|tmp|mnt/[A-Za-z])/[^\\s')]+")
	tildeFixedPathPattern   = regexp.MustCompile("(?:^|[\\s'(])~[/\\\\][^\\s')]+")
	selfInstallPatterns     = []*regexp.Regexp{
		regexp.MustCompile("(?i)\\b(?:pip3?|uv\\s+pip|python(?:3)?\\s+-m\\s+pip)\\s+install\\b"),
		regexp.MustCompile("(?i)\\b(?:npm|pnpm|yarn|bun)\\s+(?:install|add|ci)\\b"),
		regexp.MustCompile("(?i)\\b(?:apt(?:-get)?|brew|choco|winget|cargo|go)\\s+install\\b"),
		regexp.MustCompile("(?i)\\bnpx\\s+(?:--yes\\s+|-y\\s+)?[@A-Za-z0-9_.-]+"),
		regexp.MustCompile("(?i)\\b(?:curl|wget)\\b[^\\n|]{0,240}\\|\\s*(?:sh|bash)\\b"),
	}
	authorityClaimPattern              = regexp.MustCompile("(?i)\\b(?:own|owns|decide|decides|assign|assigns|lock|locks|approve|approves|authoritative|authority)\\b")
	clauseSplitPattern                 = regexp.MustCompile("(?i)[.;\\n]+|\\bbut\\b|\\bhowever\\b")
	evidenceProviderDescriptionPattern = regexp.MustCompile("(?i)\\b(?:evidence|measurement)\\s+provider\\b")
	pageProductionPattern              = regexp.MustCompile("(?i)\\b(?:write|rewrite|publish|generate|create|build|produce)\\b.{0,48}\\b(?:website page|landing page|page copy|page content|cms-ready page|production page|production content)\\b")
	pageProductionNounPattern          = regexp.MustCompile("(?i)\\b(?:production-bound page|page generation|page rewrite|page restructuring|cms-ready output|cms-ready content)\\b")
)

func validateReleaseGate(packageDir string, doc SkillDocument) ([]ReleaseGateViolation, error) {
	if !isSEOSkillName(doc.Name) {
		return nil, nil
	}
	files, err := loadReleaseGateTextFiles(packageDir)
	if err != nil {
		return nil, err
	}
	violations := validateSEOContract(doc, files)
	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].Rule != violations[j].Rule {
			return violations[i].Rule < violations[j].Rule
		}
		if violations[i].Path != violations[j].Path {
			return violations[i].Path < violations[j].Path
		}
		if violations[i].Line != violations[j].Line {
			return violations[i].Line < violations[j].Line
		}
		return violations[i].Message < violations[j].Message
	})
	return dedupeReleaseGateViolations(violations), nil
}

func enforceReleaseGate(packageDir string, doc SkillDocument) error {
	violations, err := validateReleaseGate(packageDir, doc)
	if err != nil {
		return packageError(ErrReleaseGateFailed, "release_gate.scan", err)
	}
	if len(violations) == 0 {
		return nil
	}
	return violations[0].packageError()
}

func isSEOSkillName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "seo" || strings.HasPrefix(name, "seo-")
}

func isReleaseGateLegalMetadata(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return strings.HasPrefix(upper, "LICENSE") ||
		strings.HasPrefix(upper, "COPYING") ||
		strings.HasPrefix(upper, "NOTICE")
}

func loadReleaseGateTextFiles(packageDir string) ([]releaseGateTextFile, error) {
	files := make([]releaseGateTextFile, 0)
	err := filepath.WalkDir(packageDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.Name() == ".agentdock-install.json" || isReleaseGateLegalMetadata(entry.Name()) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := releaseGateTextExtensions[ext]; !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxReleaseGateTextFileBytes {
			rel, _ := filepath.Rel(packageDir, path)
			return fmt.Errorf("release-gate text file %s exceeds %d bytes", filepath.ToSlash(rel), maxReleaseGateTextFileBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		rel, err := filepath.Rel(packageDir, path)
		if err != nil {
			return err
		}
		files = append(files, releaseGateTextFile{
			Path:  filepath.ToSlash(rel),
			Lines: strings.Split(text, "\n"),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func validateSEOContract(doc SkillDocument, files []releaseGateTextFile) []ReleaseGateViolation {
	violations := make([]ReleaseGateViolation, 0)
	violations = append(violations, validateSEOLegacyRuntime(files)...)
	violations = append(violations, validateSEOFixedPaths(files)...)
	violations = append(violations, validateSEOSelfInstall(files)...)
	violations = append(violations, validateSEOProviderAuthority(doc)...)
	violations = append(violations, validateSEOPageContractGate(doc)...)
	violations = append(violations, validateSEODuplicateAuthority(doc)...)
	return violations
}

func validateSEOLegacyRuntime(files []releaseGateTextFile) []ReleaseGateViolation {
	return scanReleaseGateLines(files, "legacy_runtime", func(line string) bool {
		return legacyRuntimePattern.MatchString(line)
	}, "legacy Claude/claude-seo runtime or command reference is not allowed")
}

func validateSEOFixedPaths(files []releaseGateTextFile) []ReleaseGateViolation {
	return scanReleaseGateLines(files, "fixed_path", func(line string) bool {
		return windowsFixedPathPattern.MatchString(line) ||
			posixFixedPathPattern.MatchString(line) ||
			tildeFixedPathPattern.MatchString(line)
	}, "host-specific absolute or home-relative path is not allowed; use Skill-relative resources or runtime-resolved paths")
}

func validateSEOSelfInstall(files []releaseGateTextFile) []ReleaseGateViolation {
	return scanReleaseGateLines(files, "self_install_dependency", func(line string) bool {
		for _, pattern := range selfInstallPatterns {
			if pattern.MatchString(line) {
				return true
			}
		}
		return false
	}, "Skill must not install or bootstrap its own dependencies at runtime")
}

func scanReleaseGateLines(files []releaseGateTextFile, rule string, matches func(string) bool, message string) []ReleaseGateViolation {
	violations := make([]ReleaseGateViolation, 0)
	for _, file := range files {
		for index, line := range file.Lines {
			if !matches(line) {
				continue
			}
			violations = append(violations, ReleaseGateViolation{
				Rule: rule, Path: file.Path, Line: index + 1, Message: message,
			})
		}
	}
	return violations
}

func validateSEOProviderAuthority(doc SkillDocument) []ReleaseGateViolation {
	if !isSEOEvidenceProvider(doc) {
		return nil
	}
	full := strings.ToLower(doc.Description + "\n" + doc.Body)
	violations := make([]ReleaseGateViolation, 0)
	if !strings.Contains(full, "evidence-only") &&
		!strings.Contains(full, "evidence layer") &&
		!strings.Contains(full, "measurement provider") &&
		!strings.Contains(full, "measurement layer") {
		violations = append(violations, ReleaseGateViolation{
			Rule: "provider_authority", Path: "SKILL.md",
			Message: "evidence provider must explicitly declare an evidence-only or measurement-only boundary",
		})
	}

	inOwnsSection := false
	for index, line := range strings.Split(doc.Body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			switch {
			case strings.Contains(heading, "does not own"), strings.Contains(heading, "doesn't own"):
				inOwnsSection = false
			case heading == "owns", strings.HasSuffix(heading, " owns"):
				inOwnsSection = true
			default:
				inOwnsSection = false
			}
			continue
		}
		if inOwnsSection && containsProviderStrategicObject(strings.ToLower(trimmed)) {
			violations = append(violations, ReleaseGateViolation{
				Rule: "provider_authority", Path: "SKILL.md", Line: index + 1,
				Message: "evidence provider Owns section claims strategy, planning, Page Contract, or implementation authority",
			})
		}
	}

	for _, clause := range splitAuthorityClauses(doc.Body) {
		lower := strings.ToLower(strings.TrimSpace(clause))
		if lower == "" || isNegativeAuthorityClause(lower) || !containsProviderStrategicObject(lower) {
			continue
		}
		if !authorityClaimPattern.MatchString(lower) || !isSelfAuthorityClaim(lower, doc.Name) {
			continue
		}
		violations = append(violations, ReleaseGateViolation{
			Rule: "provider_authority", Path: "SKILL.md",
			Message: "evidence provider may retrieve and normalize evidence but may not claim strategy, priority, Page Contract, or implementation authority",
		})
		break
	}
	return violations
}

func isSEOEvidenceProvider(doc SkillDocument) bool {
	switch strings.ToLower(strings.TrimSpace(doc.Name)) {
	case "seo-google", "seo-dataforseo", "seo-maps":
		return true
	}
	return evidenceProviderDescriptionPattern.MatchString(doc.Description)
}

func containsProviderStrategicObject(value string) bool {
	terms := []string{
		"seo strategy", "roadmap", "priority", "keyword priorit", "keyword choice",
		"url ownership", "page contract", "content plan", "content strategy",
		"remediation sequencing", "remediation choice", "local seo strategy",
		"programmatic expansion", "implementation authorization", "cms mutation", "publish",
	}
	return containsAny(value, terms...)
}

func validateSEOPageContractGate(doc SkillDocument) []ReleaseGateViolation {
	if !hasPageProductionCue(doc.Description + "\n" + doc.Body) {
		return nil
	}
	full := strings.ToLower(doc.Description + "\n" + doc.Body)
	missing := make([]string, 0, 2)
	if !strings.Contains(full, "page contract") {
		missing = append(missing, "Page Contract")
	}
	if !strings.Contains(full, "rui-website-plan") {
		missing = append(missing, "rui-website-plan authority")
	}
	if len(missing) == 0 {
		return nil
	}
	return []ReleaseGateViolation{{
		Rule: "page_contract_bypass", Path: "SKILL.md",
		Message: "page/content production capability is declared without the required " + strings.Join(missing, " and "),
	}}
}

func hasPageProductionCue(value string) bool {
	for _, clause := range splitAuthorityClauses(value) {
		lower := strings.ToLower(strings.TrimSpace(clause))
		if lower == "" || isNegativeAuthorityClause(lower) {
			continue
		}
		if pageProductionPattern.MatchString(lower) || pageProductionNounPattern.MatchString(lower) {
			return true
		}
	}
	return false
}

type protectedSEOAuthority struct {
	RuleTerms []string
	Owner     string
	Label     string
}

var protectedSEOAuthorities = []protectedSEOAuthority{
	{
		RuleTerms: []string{"url ownership", "page contract", "internal-link graph", "internal link graph", "canonical ownership", "site architecture", "page role", "page roles"},
		Owner:     "rui-website-plan", Label: "URL/IA/Page Contract authority",
	},
	{
		RuleTerms: []string{"keyword universe", "keyword prioritization", "keyword priority"},
		Owner:     "keyword-research", Label: "keyword-universe/prioritization authority",
	},
	{
		RuleTerms: []string{"shared domain semantics", "domain concepts", "canonical glossary", "shared terminology"},
		Owner:     "domain-modeling", Label: "shared domain-language authority",
	},
	{
		RuleTerms: []string{"consequential user choices", "consequential decisions", "user-owned decisions"},
		Owner:     "decision-grilling", Label: "consequential user-decision authority",
	},
}

func validateSEODuplicateAuthority(doc SkillDocument) []ReleaseGateViolation {
	violations := make([]ReleaseGateViolation, 0)
	for _, clause := range splitAuthorityClauses(doc.Body) {
		lower := strings.ToLower(strings.TrimSpace(clause))
		if lower == "" || isNegativeAuthorityClause(lower) || !authorityClaimPattern.MatchString(lower) || !isSelfAuthorityClaim(lower, doc.Name) {
			continue
		}
		for _, protected := range protectedSEOAuthorities {
			if !containsAny(lower, protected.RuleTerms...) {
				continue
			}
			if strings.Contains(lower, protected.Owner) || containsDelegationCue(lower) {
				continue
			}
			violations = append(violations, ReleaseGateViolation{
				Rule: "duplicate_authority", Path: "SKILL.md",
				Message: fmt.Sprintf("%s is already owned by %s; seo-* Skills may consume or hand off to that authority but may not duplicate it", protected.Label, protected.Owner),
			})
			break
		}
	}

	inOwnsSection := false
	for index, line := range strings.Split(doc.Body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			switch {
			case strings.Contains(heading, "does not own"), strings.Contains(heading, "doesn't own"):
				inOwnsSection = false
			case heading == "owns", strings.HasSuffix(heading, " owns"):
				inOwnsSection = true
			default:
				inOwnsSection = false
			}
			continue
		}
		if !inOwnsSection {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, protected := range protectedSEOAuthorities {
			if !containsAny(lower, protected.RuleTerms...) {
				continue
			}
			if strings.Contains(lower, protected.Owner) {
				continue
			}
			violations = append(violations, ReleaseGateViolation{
				Rule: "duplicate_authority", Path: "SKILL.md", Line: index + 1,
				Message: fmt.Sprintf("Owns section duplicates %s already owned by %s", protected.Label, protected.Owner),
			})
			break
		}
	}
	return violations
}

func splitAuthorityClauses(value string) []string {
	return clauseSplitPattern.Split(strings.ReplaceAll(value, "\r\n", "\n"), -1)
}

func isNegativeAuthorityClause(value string) bool {
	return containsAny(value,
		"does not", "doesn't", "do not", "don't", "must not", "should not",
		"never ", "cannot ", "can't ", "not own", "not decide", "not assign",
		"not lock", "not approve", "without changing", "without owning", "without deciding",
	)
}

func isSelfAuthorityClaim(value, skillName string) bool {
	trimmed := strings.TrimSpace(strings.TrimLeft(value, "-*# "))
	if strings.Contains(value, "this skill") ||
		strings.Contains(value, "this provider") ||
		strings.Contains(value, " we ") ||
		strings.HasPrefix(value, "we ") {
		return true
	}
	skillPrefix := strings.ToLower(strings.TrimSpace(skillName)) + " "
	return strings.HasPrefix(trimmed, skillPrefix) && authorityClaimPattern.MatchString(trimmed)
}

func containsDelegationCue(value string) bool {
	return containsAny(value,
		"planning authority", "planning authorities", "website planning",
		"downstream owner", "upstream authority", "hand off", "handoff",
	)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func dedupeReleaseGateViolations(input []ReleaseGateViolation) []ReleaseGateViolation {
	if len(input) < 2 {
		return input
	}
	out := make([]ReleaseGateViolation, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, violation := range input {
		key := fmt.Sprintf("%s|%s|%d|%s", violation.Rule, violation.Path, violation.Line, violation.Message)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, violation)
	}
	return out
}
