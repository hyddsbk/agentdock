package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
