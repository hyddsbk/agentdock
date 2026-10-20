package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillinterop "github.com/uvwt/agentdock/internal/skill/interop"
)

const ephemeralTestCommit = "0123456789abcdef0123456789abcdef01234567"

type fixtureSnapshotFetcher struct {
	calls        int
	lastTempRoot string
}

func (f *fixtureSnapshotFetcher) Fetch(_ context.Context, source skillinterop.SourceRef) (string, string, error) {
	f.calls++
	tempRoot, err := os.MkdirTemp("", "agentdock-ephemeral-fixture-*")
	if err != nil {
		return "", "", err
	}
	repoRoot := filepath.Join(tempRoot, "repo-"+source.Commit[:12])
	skillDir := filepath.Join(repoRoot, "skills", "demo-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o700); err != nil {
		_ = os.RemoveAll(tempRoot)
		return "", "", err
	}
	doc := `---
name: demo-skill
description: Temporary reviewed Skill.
---

# Demo

Read references before acting.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(doc), 0o600); err != nil {
		_ = os.RemoveAll(tempRoot)
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte(`# Guide

Reviewed supporting content.
`), 0o600); err != nil {
		_ = os.RemoveAll(tempRoot)
		return "", "", err
	}
	f.lastTempRoot = tempRoot
	return tempRoot, repoRoot, nil
}

func validEphemeralRequest(t *testing.T) EphemeralRequest {
	t.Helper()
	ref, err := skillinterop.ParseSourceRef("github:owner/repo@" + ephemeralTestCommit)
	if err != nil {
		t.Fatal(err)
	}
	lock := skillinterop.SourceLock{
		Schema:            skillinterop.DiscoveryLockSchema,
		Provider:          "github",
		Repository:        "owner/repo",
		Skill:             "demo-skill",
		Source:            ref,
		ReviewRequired:    true,
		InstallAuthorized: false,
		NextOwner:         "project-due-diligence",
	}
	lockSHA, err := skillinterop.SourceLockSHA256(lock)
	if err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{
		"schema":  skillinterop.AdoptionPlanSchema,
		"subject": "owner/repo",
		"verdict": "ADOPT",
		"source_snapshot": map[string]any{
			"kind":      "commit",
			"value":     ephemeralTestCommit,
			"immutable": true,
		},
		"approved_units": []string{"demo-skill"},
		"requirements": map[string]any{
			"unresolved_conditions": []string{},
		},
		"execution_allowed": true,
		"decision_binding": map[string]any{
			"review_digest": strings.Repeat("b", 64),
		},
	}
	planSHA, err := skillinterop.CanonicalJSONSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	return EphemeralRequest{
		Action:             "bind",
		Skill:              "demo-skill",
		SourceLock:         lock,
		SourceLockSHA256:   lockSHA,
		AdoptionPlan:       plan,
		AdoptionPlanSHA256: planSHA,
	}
}

func TestEphemeralBindListReadClearDoesNotInstall(t *testing.T) {
	service, _ := newSkillTestService(t)
	fetcher := &fixtureSnapshotFetcher{}
	service.ephemeral.fetcher = fetcher

	result, err := service.Ephemeral(context.Background(), validEphemeralRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := result["binding"].(EphemeralBindingInfo)
	if !ok {
		t.Fatalf("binding = %#v", result["binding"])
	}
	if binding.SourceType != "ephemeral" || binding.Skill != "demo-skill" || binding.File == "" {
		t.Fatalf("unexpected binding = %#v", binding)
	}
	if result["persistent"] != false || result["install_authorized"] != false {
		t.Fatalf("governance flags = %#v", result)
	}
	active, err := service.state.ActiveVersion("demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if active != "" {
		t.Fatalf("ephemeral bind mutated active version: %q", active)
	}
	if _, err := service.ResolveActive("demo-skill"); err == nil {
		t.Fatal("ephemeral Skill became executable as an active managed Skill")
	}

	items, err := service.CapabilityItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SourceType != "ephemeral" || items[0].File != binding.File {
		t.Fatalf("capability items = %#v", items)
	}

	absolute, display, err := service.ResolveResource(binding.File)
	if err != nil {
		t.Fatal(err)
	}
	if display != binding.File {
		t.Fatalf("display path = %q, want %q", display, binding.File)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Temporary reviewed Skill") {
		t.Fatalf("unexpected SKILL.md: %q", string(data))
	}

	listed, err := service.Ephemeral(context.Background(), EphemeralRequest{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if listed["count"] != 1 {
		t.Fatalf("list = %#v", listed)
	}

	cleared, err := service.Ephemeral(context.Background(), EphemeralRequest{Action: "clear", BindingID: binding.ID})
	if err != nil {
		t.Fatal(err)
	}
	if cleared["removed"] != true {
		t.Fatalf("clear = %#v", cleared)
	}
	if _, err := os.Stat(fetcher.lastTempRoot); !os.IsNotExist(err) {
		t.Fatalf("ephemeral temp root still exists after clear: err=%v", err)
	}
	if _, _, err := service.ResolveResource(binding.File); err == nil {
		t.Fatal("cleared ephemeral resource remained addressable")
	}
}

func TestEphemeralAuthorizationFailsBeforeFetch(t *testing.T) {
	service, _ := newSkillTestService(t)
	fetcher := &fixtureSnapshotFetcher{}
	service.ephemeral.fetcher = fetcher

	request := validEphemeralRequest(t)
	request.AdoptionPlan["verdict"] = "ADAPT"
	planSHA, err := skillinterop.CanonicalJSONSHA256(request.AdoptionPlan)
	if err != nil {
		t.Fatal(err)
	}
	request.AdoptionPlanSHA256 = planSHA

	if _, err := service.Ephemeral(context.Background(), request); err == nil {
		t.Fatal("ADAPT plan unexpectedly authorized ephemeral binding")
	}
	if fetcher.calls != 0 {
		t.Fatalf("snapshot fetched before authorization completed: calls=%d", fetcher.calls)
	}
}

func TestEphemeralRejectsManagedSkillOwnerBeforeFetch(t *testing.T) {
	service, root := newSkillTestService(t)
	source := filepath.Join(root, "managed-source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := `---
name: demo-skill
description: Installed owner.
version: 1.0.0
---

# Managed
`
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.packageTest(context.Background(), map[string]any{"action": "install", "source": source, "activate": true}); err != nil {
		t.Fatal(err)
	}

	fetcher := &fixtureSnapshotFetcher{}
	service.ephemeral.fetcher = fetcher
	if _, err := service.Ephemeral(context.Background(), validEphemeralRequest(t)); err == nil {
		t.Fatal("ephemeral binding overrode an active managed Skill")
	}
	if fetcher.calls != 0 {
		t.Fatalf("managed conflict fetched external snapshot: calls=%d", fetcher.calls)
	}
}

func TestEphemeralServiceCloseRemovesTempBindings(t *testing.T) {
	service, _ := newSkillTestService(t)
	fetcher := &fixtureSnapshotFetcher{}
	service.ephemeral.fetcher = fetcher

	if _, err := service.Ephemeral(context.Background(), validEphemeralRequest(t)); err != nil {
		t.Fatal(err)
	}
	tempRoot := fetcher.lastTempRoot
	if _, err := os.Stat(tempRoot); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempRoot); !os.IsNotExist(err) {
		t.Fatalf("temp root still exists after Service.Close: err=%v", err)
	}
	if got := service.ephemeral.list(); len(got) != 0 {
		t.Fatalf("bindings survived close: %#v", got)
	}
}

func TestExtractEphemeralZipRejectsTraversalAndSymlink(t *testing.T) {
	t.Run("traversal", func(t *testing.T) {
		archive := buildZipFixture(t, []zipFixtureEntry{
			{name: "repo/SKILL.md", body: "safe"},
			{name: "repo/../escape.txt", body: "escape"},
		})
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := extractEphemeralZip(reader); err == nil || !strings.Contains(err.Error(), "traversal") {
			t.Fatalf("expected traversal rejection, got %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		header := &zip.FileHeader{Name: "repo/link"}
		header.SetMode(os.ModeSymlink | 0o777)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("target")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := extractEphemeralZip(reader); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("expected symlink rejection, got %v", err)
		}
	})
}

func TestGitHubSnapshotFetcherUsesPinnedCommitPath(t *testing.T) {
	archive := buildZipFixture(t, []zipFixtureEntry{
		{name: "repo-root/SKILL.md", body: "---\nname: demo-skill\ndescription: Demo.\n---\n\n# Demo\n"},
	})
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	ref, err := skillinterop.ParseSourceRef("github:owner/repo@" + ephemeralTestCommit)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &githubSnapshotFetcher{client: server.Client(), baseURL: server.URL}
	tempRoot, repoRoot, err := fetcher.Fetch(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempRoot)
	if gotPath != "/owner/repo/zip/"+ephemeralTestCommit {
		t.Fatalf("snapshot path = %q", gotPath)
	}
	if info, err := os.Stat(repoRoot); err != nil || !info.IsDir() {
		t.Fatalf("repository root invalid: info=%v err=%v", info, err)
	}
}

type zipFixtureEntry struct {
	name string
	body string
}

func buildZipFixture(t *testing.T, entries []zipFixtureEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
