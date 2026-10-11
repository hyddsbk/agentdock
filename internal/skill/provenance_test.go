package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillstate "github.com/uvwt/agentdock/internal/skill/state"
)

func installProvenanceFixture(t *testing.T) (*Manager, *skillstate.Store, InstallResult) {
	t.Helper()
	state, err := skillstate.New(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(state)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: demo-skill\ndescription: Provenance fixture.\nversion: 1.0.0\n---\n\n# Demo\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := manager.Install(context.Background(), InstallRequest{Source: source, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	return manager, state, installed
}

func bindFixture(t *testing.T, manager *Manager, installed InstallResult) BindProvenanceResult {
	t.Helper()
	result, err := manager.BindProvenance(context.Background(), BindProvenanceRequest{
		Skill:                   installed.Skill,
		Version:                 installed.Version,
		PlanSHA256:              strings.Repeat("a", 64),
		ActivationReceiptSHA256: strings.Repeat("b", 64),
		ArtifactSHA256:          strings.TrimPrefix(installed.Digest, "sha256:"),
		Bundle:                  json.RawMessage(`{"plan":{"fixture":true},"chain":[{"fixture":true}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBindProvenancePersistsRegistryAndInstallMetadata(t *testing.T) {
	manager, state, installed := installProvenanceFixture(t)
	bound := bindFixture(t, manager, installed)

	selection, err := state.Snapshot(installed.Skill)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Provenance == nil {
		t.Fatal("active selection has no provenance binding")
	}
	if selection.Provenance.ArtifactSHA256 != strings.TrimPrefix(installed.Digest, "sha256:") {
		t.Fatalf("artifact digest = %q", selection.Provenance.ArtifactSHA256)
	}
	if selection.Provenance.InstalledContentSHA256 != bound.InstalledContentSHA256 {
		t.Fatalf("content digest mismatch: %#v vs %#v", selection.Provenance, bound)
	}

	metadataBytes, err := os.ReadFile(filepath.Join(installed.Path, ".agentdock-install.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata["source_digest"]) == 0 || len(metadata["content_digest"]) == 0 || len(metadata["provenance"]) == 0 {
		t.Fatalf("metadata missing provenance fields: %s", metadataBytes)
	}
}

func TestBindProvenanceRejectsTamperedInstalledContent(t *testing.T) {
	manager, _, installed := installProvenanceFixture(t)
	if err := os.WriteFile(filepath.Join(installed.Path, "SKILL.md"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := manager.BindProvenance(context.Background(), BindProvenanceRequest{
		Skill:                   installed.Skill,
		Version:                 installed.Version,
		PlanSHA256:              strings.Repeat("a", 64),
		ActivationReceiptSHA256: strings.Repeat("b", 64),
		ArtifactSHA256:          strings.TrimPrefix(installed.Digest, "sha256:"),
		Bundle:                  json.RawMessage(`{"plan":{},"chain":[{}]}`),
	})
	if err == nil || !strings.Contains(err.Error(), "installed content digest does not match install metadata") {
		t.Fatalf("tampered content bind error = %v", err)
	}
}

func TestBindProvenanceRejectsLegacyMetadataWithoutSourceDigest(t *testing.T) {
	manager, _, installed := installProvenanceFixture(t)
	metaPath := filepath.Join(installed.Path, ".agentdock-install.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	delete(metadata, "source_digest")
	legacy, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = manager.BindProvenance(context.Background(), BindProvenanceRequest{
		Skill:                   installed.Skill,
		Version:                 installed.Version,
		PlanSHA256:              strings.Repeat("a", 64),
		ActivationReceiptSHA256: strings.Repeat("b", 64),
		ArtifactSHA256:          strings.TrimPrefix(installed.Digest, "sha256:"),
		Bundle:                  json.RawMessage(`{"plan":{},"chain":[{}]}`),
	})
	if err == nil || !strings.Contains(err.Error(), "legacy installation cannot be provenance-bound") {
		t.Fatalf("legacy metadata bind error = %v", err)
	}
}

func TestActivationClearsStaleProvenanceBinding(t *testing.T) {
	manager, state, installed := installProvenanceFixture(t)
	bindFixture(t, manager, installed)
	if err := state.Activate(context.Background(), installed.Skill, installed.Version); err != nil {
		t.Fatal(err)
	}
	selection, err := state.Snapshot(installed.Skill)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Provenance != nil {
		t.Fatalf("ordinary activation retained stale provenance: %#v", selection.Provenance)
	}
}
