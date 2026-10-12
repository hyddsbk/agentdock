package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
	"github.com/uvwt/agentdock/internal/nexusbridge"
)

func TestAuditVerifyCommandFailsClosedWithMachineReadableReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(path, []byte(`{"contract":"provenance-audit-bundle/v1","manifest":{},"task":{},"graph":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runAuditCommand([]string{"verify", path}, &out)
	if err == nil || !strings.Contains(err.Error(), "integrity verification failed") {
		t.Fatalf("verify error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, `"contract": "provenance-integrity/v1"`) || !strings.Contains(text, `"valid": false`) {
		t.Fatalf("audit report = %s", text)
	}
}

func TestAuditVerifyCommandRejectsBadInvocation(t *testing.T) {
	var out bytes.Buffer
	if err := runAuditCommand([]string{"verify"}, &out); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("usage error = %v", err)
	}
}

func TestAuditTrustCLIInitializesRotatesRevokesAndExportsPublicRegistry(t *testing.T) {
	home := t.TempDir()
	if err := nexusbridge.Save(home, nexusbridge.Identity{
		Version: 1, Endpoint: "http://127.0.0.1:18777",
		NodeID: "node_cli", DeviceID: "device_cli", DeviceToken: "test-device-token",
	}); err != nil {
		t.Fatal(err)
	}

	var initOut bytes.Buffer
	if err := runAuditCommand([]string{"trust", "init", "--home", home}, &initOut); err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal(initOut.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	rootID, _ := summary["root_key_id"].(string)
	activeID, _ := summary["active_key_id"].(string)
	if !strings.HasPrefix(rootID, "audit-root-sha256:") || !strings.HasPrefix(activeID, "audit-key-sha256:") {
		t.Fatalf("trust summary = %#v", summary)
	}
	if strings.Contains(initOut.String(), "private_key") {
		t.Fatalf("private key leaked in CLI output: %s", initOut.String())
	}

	var rotateOut bytes.Buffer
	if err := runAuditCommand([]string{"trust", "rotate", "--home", home}, &rotateOut); err != nil {
		t.Fatal(err)
	}
	var rotated map[string]any
	if err := json.Unmarshal(rotateOut.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	newActive, _ := rotated["active_key_id"].(string)
	if newActive == "" || newActive == activeID || rotated["root_key_id"] != rootID {
		t.Fatalf("rotation summary = %#v", rotated)
	}

	var revokeOut bytes.Buffer
	if err := runAuditCommand([]string{"trust", "revoke", activeID, "--home", home}, &revokeOut); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(t.TempDir(), "registry.json")
	var exportOut bytes.Buffer
	if err := runAuditCommand([]string{"trust", "export", "--home", home, "--out", registryPath}, &exportOut); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"contract": "audit-trust-registry/v1"`) || !strings.Contains(text, `"status": "revoked"`) {
		t.Fatalf("registry export = %s", text)
	}
	if strings.Contains(text, "private_key") {
		t.Fatalf("private key leaked in public registry: %s", text)
	}
}

func TestAuditTrustCheckpointCLICreatesPortableFreshnessSnapshot(t *testing.T) {
	home := t.TempDir()
	if err := nexusbridge.Save(home, nexusbridge.Identity{
		Version: 1, Endpoint: "http://127.0.0.1:18777",
		NodeID: "node_p11_cli", DeviceID: "device_p11_cli", DeviceToken: "test-device-token",
	}); err != nil {
		t.Fatal(err)
	}
	var initOut bytes.Buffer
	if err := runAuditCommand([]string{"trust", "init", "--home", home}, &initOut); err != nil {
		t.Fatal(err)
	}
	var initSummary map[string]any
	if err := json.Unmarshal(initOut.Bytes(), &initSummary); err != nil {
		t.Fatal(err)
	}
	rootID, _ := initSummary["root_key_id"].(string)

	snapshotPath := filepath.Join(t.TempDir(), "checkpoint.json")
	var checkpointOut bytes.Buffer
	if err := runAuditCommand([]string{
		"trust", "checkpoint", "create",
		"--home", home,
		"--fresh-for", "1h",
		"--out", snapshotPath,
	}, &checkpointOut); err != nil {
		t.Fatal(err)
	}
	snapshot, err := nexusbridge.LoadAuditTrustCheckpointSnapshot(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Registry.RootKeyID != rootID || snapshot.Checkpoint.Statement.RegistryRevision != snapshot.Registry.Revision {
		t.Fatalf("checkpoint snapshot = %#v", snapshot)
	}
	if report := audittrust.VerifyCheckpoint(snapshot.Registry, snapshot.Checkpoint, rootID, time.Now()); !report.Valid {
		t.Fatalf("checkpoint verification = %#v", report)
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private_key") {
		t.Fatalf("private key leaked in checkpoint snapshot: %s", data)
	}
}

func TestAuditTrustStateCLIPersistsRevisionAndAppliesRootContinuity(t *testing.T) {
	signerHome := t.TempDir()
	verifierHome := t.TempDir()
	if err := nexusbridge.Save(signerHome, nexusbridge.Identity{
		Version: 1, Endpoint: "http://127.0.0.1:18777",
		NodeID: "node_p10_cli", DeviceID: "device_p10_cli", DeviceToken: "test-device-token",
	}); err != nil {
		t.Fatal(err)
	}

	var initOut bytes.Buffer
	if err := runAuditCommand([]string{"trust", "init", "--home", signerHome}, &initOut); err != nil {
		t.Fatal(err)
	}
	var initSummary map[string]any
	if err := json.Unmarshal(initOut.Bytes(), &initSummary); err != nil {
		t.Fatal(err)
	}
	oldRootID, _ := initSummary["root_key_id"].(string)
	if oldRootID == "" {
		t.Fatalf("init summary = %#v", initSummary)
	}

	dir := t.TempDir()
	oldRegistryPath := filepath.Join(dir, "registry-v1.json")
	if err := runAuditCommand([]string{"trust", "export", "--home", signerHome, "--out", oldRegistryPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var bootstrapOut bytes.Buffer
	if err := runAuditCommand([]string{
		"trust-state", "bootstrap",
		"--home", verifierHome,
		"--trust-registry", oldRegistryPath,
		"--trust-root", oldRootID,
		"--trust-revision", "1",
	}, &bootstrapOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bootstrapOut.String(), `"contract": "audit-trust-state/v1"`) ||
		!strings.Contains(bootstrapOut.String(), `"highest_accepted_registry_revision": 1`) {
		t.Fatalf("bootstrap state = %s", bootstrapOut.String())
	}

	continuityPath := filepath.Join(dir, "root-continuity.json")
	var rolloverOut bytes.Buffer
	if err := runAuditCommand([]string{
		"trust", "rollover-root",
		"--home", signerHome,
		"--continuity", continuityPath,
	}, &rolloverOut); err != nil {
		t.Fatal(err)
	}
	var rolloverSummary map[string]any
	if err := json.Unmarshal(rolloverOut.Bytes(), &rolloverSummary); err != nil {
		t.Fatal(err)
	}
	newRootID, _ := rolloverSummary["root_key_id"].(string)
	if newRootID == "" || newRootID == oldRootID {
		t.Fatalf("root rollover summary = %#v", rolloverSummary)
	}
	continuityData, err := os.ReadFile(continuityPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(continuityData), `"contract": "audit-root-continuity/v1"`) ||
		!strings.Contains(string(continuityData), `"previous_root_signature"`) ||
		!strings.Contains(string(continuityData), `"next_root_signature"`) {
		t.Fatalf("continuity document = %s", continuityData)
	}

	newRegistryPath := filepath.Join(dir, "registry-v2.json")
	if err := runAuditCommand([]string{"trust", "export", "--home", signerHome, "--out", newRegistryPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var applyOut bytes.Buffer
	if err := runAuditCommand([]string{
		"trust-state", "apply-rollover",
		"--home", verifierHome,
		"--continuity", continuityPath,
		"--trust-registry", newRegistryPath,
	}, &applyOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applyOut.String(), newRootID) ||
		!strings.Contains(applyOut.String(), `"highest_accepted_registry_revision": 2`) {
		t.Fatalf("applied state = %s", applyOut.String())
	}

	var statusOut bytes.Buffer
	if err := runAuditCommand([]string{"trust-state", "status", "--home", verifierHome}, &statusOut); err != nil {
		t.Fatal(err)
	}
	if statusOut.String() != applyOut.String() {
		t.Fatalf("persisted state differs from applied state:\napply=%s\nstatus=%s", applyOut.String(), statusOut.String())
	}
}
