package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skills "github.com/uvwt/agentdock/internal/skill"
)

func TestSkillPackageBindProvenanceMarksActiveSelection(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "demo-provenance")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: demo-provenance\ndescription: Provenance tool fixture.\nversion: 1.0.0\n---\n\n# Demo\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	service, _ := newSkillTestServiceAtRoot(t, root)
	installed, err := service.packageTest(context.Background(), map[string]any{
		"action": "install", "source": "demo-provenance", "activate": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	installResult, ok := installed["result"].(skills.InstallResult)
	if !ok {
		t.Fatalf("install result = %#v", installed["result"])
	}
	bound, err := service.packageTest(context.Background(), map[string]any{
		"action":                    "bind_provenance",
		"skill":                     installResult.Skill,
		"version":                   installResult.Version,
		"plan_sha256":               strings.Repeat("a", 64),
		"activation_receipt_sha256": strings.Repeat("b", 64),
		"artifact_sha256":           strings.TrimPrefix(installResult.Digest, "sha256:"),
		"provenance_bundle": map[string]any{
			"plan":  map[string]any{"fixture": true},
			"chain": []any{map[string]any{"fixture": true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bound["action"] != "bind_provenance" {
		t.Fatalf("bind response = %#v", bound)
	}
	result, ok := bound["result"].(skills.BindProvenanceResult)
	if !ok || result.Skill != installResult.Skill {
		t.Fatalf("bind result = %#v", bound["result"])
	}
	listed, err := service.list()
	if err != nil {
		t.Fatal(err)
	}
	items := listed["skills"].([]map[string]any)
	if len(items) != 1 || items[0]["provenance_bound"] != true {
		t.Fatalf("list provenance state = %#v", items)
	}
}
