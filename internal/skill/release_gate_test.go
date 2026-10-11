package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillstate "github.com/uvwt/agentdock/internal/skill/state"
)

func TestSEOReleaseGateRuleFamilies(t *testing.T) {
	tests := []struct {
		name        string
		skillName   string
		description string
		body        string
		extraFile   string
		extraBody   string
		wantRule    string
	}{
		{
			name:        "provider authority overreach",
			skillName:   "seo-provider-test",
			description: "SEO evidence provider for test measurements.",
			body:        "# Provider\n\nThis Skill is evidence-only.\n\nThis Skill owns SEO strategy and roadmap priority.",
			wantRule:    "provider_authority",
		},
		{
			name:        "page contract bypass",
			skillName:   "seo-writer-test",
			description: "Write and publish website page copy for SEO.",
			body:        "# Writer\n\nCreate landing page copy and publish pages directly.",
			wantRule:    "page_contract_bypass",
		},
		{
			name:        "legacy runtime",
			skillName:   "seo-legacy-test",
			description: "SEO analysis helper.",
			body:        "# Legacy\n\nUse existing runtime tools.",
			extraFile:   "references/legacy.md",
			extraBody:   "Run claude-seo audit for the final check.\n",
			wantRule:    "legacy_runtime",
		},
		{
			name:        "fixed path",
			skillName:   "seo-path-test",
			description: "SEO analysis helper.",
			body:        "# Paths\n\nRead references.",
			extraFile:   "references/path.md",
			extraBody:   "Read C:\\Users\\someone\\seo\\config.json before analysis.\n",
			wantRule:    "fixed_path",
		},
		{
			name:        "self install dependency",
			skillName:   "seo-install-test",
			description: "SEO analysis helper.",
			body:        "# Install\n\nUse connected tools.",
			extraFile:   "references/setup.md",
			extraBody:   "pip install dataforseo\n",
			wantRule:    "self_install_dependency",
		},
		{
			name:        "duplicate authority",
			skillName:   "seo-owner-test",
			description: "SEO planning helper.",
			body:        "# Owner\n\nThis Skill owns URL ownership and Page Contracts.",
			wantRule:    "duplicate_authority",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := writeSEOReleaseGateFixture(t, test.skillName, test.description, test.body, test.extraFile, test.extraBody)
			doc, err := LoadSkillDocument(source)
			if err != nil {
				t.Fatal(err)
			}
			violations, err := validateReleaseGate(source, doc)
			if err != nil {
				t.Fatal(err)
			}
			if !hasReleaseGateRule(violations, test.wantRule) {
				t.Fatalf("violations = %#v, want rule %q", violations, test.wantRule)
			}
		})
	}
}

func TestSEOReleaseGateAcceptsLayeredContracts(t *testing.T) {
	body := "# Provider\n\n" +
		"This Skill is an evidence-only measurement provider.\n\n" +
		"## Provider boundary\n\n" +
		"### Owns\n\n" +
		"- measurement retrieval;\n" +
		"- scope and provenance.\n\n" +
		"### Does not own\n\n" +
		"- SEO strategy;\n" +
		"- keyword priority;\n" +
		"- URL ownership or Page Contracts;\n" +
		"- implementation authorization.\n\n" +
		"Hand planning decisions to rui-website-plan, keyword-research, seo-plan, and other downstream owners.\n"
	source := writeSEOReleaseGateFixture(
		t,
		"seo-provider-clean",
		"SEO evidence provider for scoped measurements.",
		body,
		"",
		"",
	)
	doc, err := LoadSkillDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := validateReleaseGate(source, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("clean layered provider rejected: %#v", violations)
	}
}

func TestSEOReleaseGateDoesNotAffectNonSEO(t *testing.T) {
	source := writeSEOReleaseGateFixture(
		t,
		"demo-skill",
		"Non SEO demo.",
		"# Demo\n\nThis Skill owns URL ownership and Page Contracts.\n\npip install demo\n",
		"",
		"",
	)
	doc, err := LoadSkillDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := validateReleaseGate(source, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("non-SEO Skill should be out of scope: %#v", violations)
	}
}

func TestValidateReportsSEOReleaseGateIssues(t *testing.T) {
	state, err := skillstate.New(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(state)
	if err != nil {
		t.Fatal(err)
	}
	source := writeSEOReleaseGateFixture(
		t,
		"seo-invalid-validate",
		"SEO evidence provider for measurements.",
		"# Provider\n\nThis Skill owns SEO strategy.\n",
		"references/setup.md",
		"npm install hidden-runtime\n",
	)
	result, err := manager.Validate(context.Background(), ValidateRequest{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatalf("invalid SEO package unexpectedly valid: %#v", result)
	}
	if !hasValidateStage(result.Issues, "release_gate.seo.provider_authority") ||
		!hasValidateStage(result.Issues, "release_gate.seo.self_install_dependency") {
		t.Fatalf("missing release-gate issues: %#v", result.Issues)
	}
}

func TestInstallCannotBypassSEOReleaseGate(t *testing.T) {
	state, err := skillstate.New(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(state)
	if err != nil {
		t.Fatal(err)
	}
	source := writeSEOReleaseGateFixture(
		t,
		"seo-invalid-install",
		"SEO analysis helper.",
		"# Invalid\n\nUse C:\\Users\\someone\\fixed\\config.json.\n",
		"",
		"",
	)
	_, err = manager.Install(context.Background(), InstallRequest{Source: source, Activate: false})
	assertReleaseGateError(t, err, "release_gate.seo.fixed_path")
}

func TestActivateRechecksSEOReleaseGate(t *testing.T) {
	state, err := skillstate.New(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(state)
	if err != nil {
		t.Fatal(err)
	}

	path, err := state.InstalledPath("seo-old-version", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	document := "---\n" +
		"name: seo-old-version\n" +
		"description: Old SEO helper.\n" +
		"version: 1.0.0\n" +
		"---\n\n" +
		"# Old\n\n" +
		"Run claude-seo audit.\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = manager.Activate(context.Background(), "seo-old-version", "1.0.0")
	assertReleaseGateError(t, err, "release_gate.seo.legacy_runtime")
}

func TestSEOReleaseGateIgnoresLegalAttributionFiles(t *testing.T) {
	source := writeSEOReleaseGateFixture(
		t,
		"seo-legal-clean",
		"SEO analysis helper.",
		"# Helper\n\nUse current AgentDock capabilities.\n",
		"LICENSE.txt",
		"Copyright upstream\nhttps://github.com/example/claude-seo\n",
	)
	doc, err := LoadSkillDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := validateReleaseGate(source, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("legal attribution should not be treated as runtime contract: %#v", violations)
	}
}

func TestSEOReleaseGateExternalSkills(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("AGENTDOCK_SEO_SKILLS_ROOT"))
	if root == "" {
		t.Skip("AGENTDOCK_SEO_SKILLS_ROOT is not set")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() || !isSEOSkillName(entry.Name()) {
			continue
		}
		source := filepath.Join(root, entry.Name())
		doc, err := LoadSkillDocument(source)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		violations, err := validateReleaseGate(source, doc)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if len(violations) != 0 {
			t.Errorf("%s rejected by SEO release gate: %#v", entry.Name(), violations)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no seo / seo-* Skills found under AGENTDOCK_SEO_SKILLS_ROOT")
	}
}

func writeSEOReleaseGateFixture(t *testing.T, name, description, body, extraFile, extraBody string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: " + name + "\ndescription: " + description + "\nversion: 1.0.0\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	if extraFile != "" {
		target := filepath.Join(source, filepath.FromSlash(extraFile))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(extraBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

func hasReleaseGateRule(violations []ReleaseGateViolation, rule string) bool {
	for _, violation := range violations {
		if violation.Rule == rule {
			return true
		}
	}
	return false
}

func hasValidateStage(issues []ValidateIssue, stage string) bool {
	for _, issue := range issues {
		if issue.Stage == stage {
			return true
		}
	}
	return false
}

func assertReleaseGateError(t *testing.T, err error, stage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected release-gate error at %s", stage)
	}
	var packageErr *Error
	if !errors.As(err, &packageErr) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if packageErr.Code != ErrReleaseGateFailed || packageErr.Stage != stage {
		t.Fatalf("error = %#v, want code=%s stage=%s", packageErr, ErrReleaseGateFailed, stage)
	}
}
