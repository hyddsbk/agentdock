package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeSkillContractExposesCurrentContentIdentity(t *testing.T) {
	service, root := newSkillTestService(t)
	source := filepath.Join(root, "runtime-contract-skill")
	if err := os.MkdirAll(filepath.Join(source, "references"), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: runtime-contract-skill\ndescription: Runtime contract fixture.\nversion: 1.0.0\n---\n\n# Runtime Contract\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "references", "guide.md"), []byte("# Guide\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.packageTest(context.Background(), map[string]any{
		"action": "install", "source": "runtime-contract-skill", "activate": true,
	}); err != nil {
		t.Fatal(err)
	}

	listed, err := service.RuntimeSkills()
	if err != nil {
		t.Fatal(err)
	}
	items, ok := listed["skills"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("RuntimeSkills items = %#v", listed["skills"])
	}
	item := items[0]
	if item["skill_ref"] != "skill://managed/runtime-contract-skill" || item["source_type"] != "managed" {
		t.Fatalf("RuntimeSkills provenance identity = %#v", item)
	}
	digest, _ := item["content_digest"].(string)
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("RuntimeSkills content_digest = %q", digest)
	}
	if item["active_version"] != "1.0.0" {
		t.Fatalf("RuntimeSkills must preserve legacy active_version for older clients: %#v", item)
	}

	detail, err := service.RuntimeSkill("runtime-contract-skill")
	if err != nil {
		t.Fatal(err)
	}
	if detail["name"] != "runtime-contract-skill" || detail["description"] != "Runtime contract fixture." ||
		detail["skill_ref"] != "skill://managed/runtime-contract-skill" || detail["source_type"] != "managed" ||
		detail["content_digest"] != digest {
		t.Fatalf("RuntimeSkill current-content identity = %#v", detail)
	}
	files, ok := detail["files"].([]runtimeSkillFile)
	if !ok || len(files) != 2 {
		t.Fatalf("RuntimeSkill files = %#v", detail["files"])
	}
}
