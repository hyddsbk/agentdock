package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := runAuditCommand([]string{"verify"}, &out); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("usage error = %v", err)
	}
}
