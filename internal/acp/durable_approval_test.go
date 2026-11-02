package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type persistedApprovalProbe struct {
	ApprovalID       string    `json:"approval_id"`
	RequestSHA256    string    `json:"request_sha256"`
	ToolCallID       string    `json:"tool_call_id,omitempty"`
	Status           string    `json:"status"`
	SelectedOptionID string    `json:"selected_option_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func waitPendingPermission(t *testing.T, manager *Manager, sessionID string) Interaction {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := manager.ListInteractions(sessionID, true)
		if len(items) > 0 {
			return items[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pending permission was not exposed")
	return Interaction{}
}

func readPersistedApprovals(t *testing.T, manager *Manager, sessionID string) []persistedApprovalProbe {
	t.Helper()
	record, err := manager.store.Get(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Approvals []persistedApprovalProbe `json:"approvals"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	return raw.Approvals
}

func expectedPermissionDigest(t *testing.T, remoteSessionID string, toolCall map[string]any, options []PermissionOption) string {
	t.Helper()
	payload := struct {
		SessionID string             `json:"session_id"`
		ToolCall  map[string]any     `json:"tool_call"`
		Options   []PermissionOption `json:"options"`
	}{
		SessionID: remoteSessionID,
		ToolCall:  toolCall,
		Options:   options,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func TestDurableApprovalPendingPersistsBeforeExposure(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	manager, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.StartPrompt(context.Background(), created.Session.ID, "durable approval")
	if err != nil {
		t.Fatal(err)
	}
	permission := waitPendingPermission(t, manager, created.Session.ID)

	approvals := readPersistedApprovals(t, manager, created.Session.ID)
	if len(approvals) != 1 {
		t.Fatalf("persisted approvals = %#v, want one pending envelope", approvals)
	}
	got := approvals[0]
	if got.ApprovalID != permission.ID || got.Status != "pending" || got.ToolCallID != "tool-1" {
		t.Fatalf("pending approval envelope = %#v", got)
	}
	if len(got.RequestSHA256) != 64 {
		t.Fatalf("request digest = %q, want 64 hex chars", got.RequestSHA256)
	}
	if _, err := hex.DecodeString(got.RequestSHA256); err != nil {
		t.Fatalf("request digest is not hex: %v", err)
	}
	wantDigest := expectedPermissionDigest(t, created.Session.RemoteSessionID, permission.ToolCall, permission.Options)
	if got.RequestSHA256 != wantDigest {
		t.Fatalf("request digest = %q want %q", got.RequestSHA256, wantDigest)
	}

	events, err := manager.PromptEvents(context.Background(), started.RunID, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if events.Status != RunRunning {
		t.Fatalf("run completed before approval: %s", events.Status)
	}

	if _, err := manager.RespondInteraction(permission.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	result := waitForSettledRun(t, manager, started.RunID)
	if result.Status != RunCompleted {
		t.Fatalf("run after live approval = %s", result.Status)
	}
}

func TestDurableApprovalDigestMismatchFailsClosed(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	manager, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.StartPrompt(context.Background(), created.Session.ID, "digest mismatch")
	if err != nil {
		t.Fatal(err)
	}
	permission := waitPendingPermission(t, manager, created.Session.ID)

	record, err := manager.store.Get(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Approvals) != 1 {
		t.Fatalf("approval count = %d, want 1", len(record.Approvals))
	}
	record.Approvals[0].RequestSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := manager.store.Save(record); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.sessions[created.Session.ID] = record
	manager.mu.Unlock()

	if _, err := manager.RespondInteraction(permission.ID, "allow-once", false); err == nil || errorCode(err) != "ACP_PERMISSION_DIGEST_MISMATCH" {
		t.Fatalf("digest mismatch response error = %#v", err)
	}
	snapshot, err := manager.PromptEvents(context.Background(), started.RunID, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != RunRunning {
		t.Fatalf("digest mismatch unexpectedly released protected action: %s", snapshot.Status)
	}
}

func TestDurableApprovalDoesNotPersistFullToolCallPayload(t *testing.T) {
	manager := newPermissionTestManager(t, time.Second)
	secret := "SENSITIVE-" + strings.Repeat("payload-", 1024)
	resultCh := startPermissionRequest(t, manager, permissionParams(map[string]any{
		"toolCallId": "tool-sensitive",
		"title":      "write file",
		"kind":       "edit",
		"payload":    secret,
	}))
	interaction := waitForPendingInteraction(t, manager)
	record, err := manager.store.Get("local-session")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), strings.Repeat("payload-", 128)) {
		t.Fatal("durable approval persisted arbitrary tool-call payload")
	}
	if len(record.Approvals) != 1 || record.Approvals[0].ToolCallID != "tool-sensitive" {
		t.Fatalf("compact approval metadata = %#v", record.Approvals)
	}
	if _, err := manager.RespondInteraction(interaction.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	assertPermissionOutcome(t, result.value, result.err, "selected", "allow-once")
}

func TestDurableApprovalRecoveryInterruptsDecisionRecorded(t *testing.T) {
	home := t.TempDir()
	store, err := newSessionStore(home, "helper")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := SessionRecord{
		SchemaVersion:   sessionSchemaVersion,
		ID:              "acps_decision_crash",
		Agent:           "helper",
		RemoteSessionID: "remote-decision-crash",
		CWD:             t.TempDir(),
		Status:          SessionRunning,
		CreatedAt:       now,
		UpdatedAt:       now,
		Approvals: []ApprovalEnvelope{{
			ApprovalID:       "acpi_decision_crash",
			RequestSHA256:    strings.Repeat("a", 64),
			ToolCallID:       "tool-1",
			Status:           ApprovalDecisionRecorded,
			SelectedOptionID: "allow-once",
			CreatedAt:        now,
			UpdatedAt:        now,
			ExpiresAt:        now.Add(time.Minute),
		}},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	recoveredStore, err := newSessionStore(home, "helper")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := recoveredStore.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != SessionInterrupted {
		t.Fatalf("session recovery status = %s", recovered.Status)
	}
	if len(recovered.Approvals) != 1 || recovered.Approvals[0].Status != ApprovalInterrupted {
		t.Fatalf("decision-recorded approval recovery = %#v", recovered.Approvals)
	}
	if recovered.Approvals[0].SelectedOptionID != "allow-once" {
		t.Fatalf("interrupted audit lost selected option = %#v", recovered.Approvals[0])
	}
}

func TestPermissionRestartRequiresFreshApproval(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	manager, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.StartPrompt(context.Background(), created.Session.ID, "first permission before restart")
	if err != nil {
		t.Fatal(err)
	}
	first := waitPendingPermission(t, manager, created.Session.ID)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	firstResult := waitForSettledRun(t, manager, started.RunID)
	if firstResult.Status != RunInterrupted {
		t.Fatalf("first run after manager close = %s", firstResult.Status)
	}

	restarted, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()

	approvals := readPersistedApprovals(t, restarted, created.Session.ID)
	if len(approvals) != 1 || approvals[0].ApprovalID != first.ID || approvals[0].Status != "interrupted" {
		t.Fatalf("restart approval state = %#v", approvals)
	}
	if _, err := restarted.RespondInteraction(first.ID, "allow-once", false); err == nil {
		t.Fatal("interrupted approval unexpectedly remained live")
	}

	secondRun, err := restarted.StartPrompt(context.Background(), created.Session.ID, "reissued permission after restart")
	if err != nil {
		t.Fatal(err)
	}
	second := waitPendingPermission(t, restarted, created.Session.ID)
	if second.ID == first.ID {
		t.Fatalf("reissued permission reused interrupted approval id %q", first.ID)
	}
	before, err := restarted.PromptEvents(context.Background(), secondRun.RunID, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if before.Status != RunRunning {
		t.Fatalf("reissued permission was silently answered: run status=%s", before.Status)
	}
	if _, err := restarted.RespondInteraction(second.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	if result := waitForSettledRun(t, restarted, secondRun.RunID); result.Status != RunCompleted {
		t.Fatalf("reissued run after fresh approval = %s", result.Status)
	}
}

func TestPermissionReissueDoesNotReplayPriorDecision(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	manager, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := manager.StartPrompt(context.Background(), created.Session.ID, "approve once")
	if err != nil {
		t.Fatal(err)
	}
	first := waitPendingPermission(t, manager, created.Session.ID)
	if _, err := manager.RespondInteraction(first.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	if result := waitForSettledRun(t, manager, firstRun.RunID); result.Status != RunCompleted {
		t.Fatalf("first run = %s", result.Status)
	}
	approvals := readPersistedApprovals(t, manager, created.Session.ID)
	if len(approvals) != 1 || approvals[0].Status != "responded" || approvals[0].SelectedOptionID != "allow-once" {
		t.Fatalf("settled durable approval = %#v", approvals)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	secondRun, err := restarted.StartPrompt(context.Background(), created.Session.ID, "same-looking permission must be fresh")
	if err != nil {
		t.Fatal(err)
	}
	second := waitPendingPermission(t, restarted, created.Session.ID)
	if second.ID == first.ID {
		t.Fatalf("fresh permission reused old approval id %q", first.ID)
	}
	snapshot, err := restarted.PromptEvents(context.Background(), secondRun.RunID, 0, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != RunRunning {
		t.Fatalf("prior decision was replayed automatically: %s", snapshot.Status)
	}
	if _, err := restarted.RespondInteraction(second.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	if result := waitForSettledRun(t, restarted, secondRun.RunID); result.Status != RunCompleted {
		t.Fatalf("second run = %s", result.Status)
	}
}
