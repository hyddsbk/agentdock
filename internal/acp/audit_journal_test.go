package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

func eventJSONField(t *testing.T, event Event, key string) any {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	return raw[key]
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestEventLineagePromotesStableIDsAndUpdateDigest(t *testing.T) {
	run := newRun("acpr_lineage", "acps_lineage")
	manager := &Manager{
		remoteToLocal:      map[string]string{"remote-session": run.SessionID},
		activeRunBySession: map[string]string{run.SessionID: run.ID},
		runs:               map[string]*Run{run.ID: run},
	}
	toolUpdate := json.RawMessage("{\"sessionId\":\"remote-session\",\"update\":{\"sessionUpdate\":\"tool_call_update\",\"toolCallId\":\"tool-42\",\"status\":\"in_progress\"}}")
	manager.handleNotification("session/update", toolUpdate)
	messageUpdate := json.RawMessage("{\"sessionId\":\"remote-session\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"messageId\":\"msg-7\",\"content\":{\"type\":\"text\",\"text\":\"public answer\"}}}")
	manager.handleNotification("session/update", messageUpdate)

	page, err := run.eventsAfter(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("events = %#v", page.Events)
	}
	if got := eventJSONField(t, page.Events[0], "tool_call_id"); got != "tool-42" {
		t.Fatalf("tool lineage = %#v, want tool-42", got)
	}
	if got := eventJSONField(t, page.Events[1], "message_id"); got != "msg-7" {
		t.Fatalf("message lineage = %#v, want msg-7", got)
	}
	if got := eventJSONField(t, page.Events[0], "update_sha256"); got != sha256Hex(page.Events[0].Update) {
		t.Fatalf("tool update digest = %#v", got)
	}
	if got := eventJSONField(t, page.Events[1], "update_sha256"); got != sha256Hex(page.Events[1].Update) {
		t.Fatalf("message update digest = %#v", got)
	}
	if eventJSONField(t, page.Events[0], "update_omitted") != nil || len(page.Events[0].Update) == 0 {
		t.Fatal("live event unexpectedly omitted update content")
	}
}

func TestAuditJournalPersistenceFailureDoesNotExposeEvent(t *testing.T) {
	run := newRun("acpr_journal_fail", "acps_journal_fail")
	run.persistEvent = func(Event) error { return errors.New("simulated audit write failure") }
	if err := run.appendEvent(Event{
		Type:   "tool_call_update",
		Update: json.RawMessage("{\"toolCallId\":\"tool-fail\"}"),
	}); err == nil {
		t.Fatal("audit persistence failure was not returned")
	}
	page, err := run.eventsAfter(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.LatestSeq != 0 || page.NextSeq != 0 {
		t.Fatalf("failed audit event became publicly visible: %#v", page)
	}
}

func TestAuditJournalPermissionPayloadIsDigestOnly(t *testing.T) {
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
	run := newRun("acpr_permission_audit", created.Session.ID)
	if err := manager.runJournal.Start(run); err != nil {
		t.Fatal(err)
	}
	run.persistEvent = manager.runJournal.Append
	manager.mu.Lock()
	manager.runs[run.ID] = run
	manager.activeRunBySession[created.Session.ID] = run.ID
	manager.mu.Unlock()

	secret := "AUDIT-PAYLOAD-SECRET-" + strings.Repeat("payload-", 256)
	params := testMarshalRaw(map[string]any{
		"sessionId": created.Session.RemoteSessionID,
		"toolCall": map[string]any{
			"toolCallId": "tool-secret",
			"title":      "write secret file",
			"kind":       "edit",
			"payload":    secret,
		},
		"options": []map[string]any{
			{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
			{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
		},
	})
	resultCh := make(chan permissionResult, 1)
	go func() {
		value, err := manager.handlePermission(context.Background(), params)
		resultCh <- permissionResult{value: value, err: err}
	}()

	var permission Interaction
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pending := manager.ListInteractions(created.Session.ID, true)
		if len(pending) == 1 {
			permission = pending[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if permission.ID == "" {
		t.Fatal("permission interaction did not become pending")
	}
	page, err := run.eventsAfter(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Type != "permission_request" ||
		page.Events[0].ToolCallID != "tool-secret" || !strings.Contains(string(page.Events[0].Update), secret) {
		t.Fatalf("live permission event = %#v", page.Events)
	}
	wantDigest := page.Events[0].UpdateSHA256
	if wantDigest == "" {
		t.Fatal("live permission event is missing update digest")
	}

	var persisted []byte
	if err := filepath.Walk(manager.runJournal.runDir(run.ID), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		persisted = append(persisted, data...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), secret) || strings.Contains(string(persisted), strings.Repeat("payload-", 64)) {
		t.Fatal("audit journal persisted raw permission tool payload")
	}
	if !strings.Contains(string(persisted), wantDigest) || !strings.Contains(string(persisted), "tool-secret") {
		t.Fatalf("audit journal missing digest/lineage: %s", persisted)
	}

	if _, err := manager.RespondInteraction(permission.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	assertPermissionOutcome(t, result.value, result.err, "selected", "allow-once")
}

func TestAuditJournalCompletedRunRecoversWithoutTranscriptPayload(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	manager, err := newTestManagerWithPromptMode(home, workspace, "helper-acp", "steering_prompt_required")
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.StartPrompt(context.Background(), created.Session.ID, "complete durable audit")
	if err != nil {
		t.Fatal(err)
	}
	live := waitForSettledRun(t, manager, started.RunID)
	if live.Status != RunCompleted {
		t.Fatalf("live run = %#v", live)
	}
	var liveChunk Event
	for _, event := range live.Events {
		if event.Type == "agent_message_chunk" {
			liveChunk = event
			break
		}
	}
	if len(liveChunk.Update) == 0 || !strings.Contains(string(liveChunk.Update), "STEERED") {
		t.Fatalf("live chunk = %#v", liveChunk)
	}
	wantDigest := sha256Hex(liveChunk.Update)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	root := manager.store.root
	var persisted []byte
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		persisted = append(persisted, data...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), "STEERED") {
		t.Fatal("durable ACP state persisted transcript payload")
	}
	if !strings.Contains(string(persisted), wantDigest) {
		t.Fatalf("durable ACP state missing update digest %s", wantDigest)
	}

	restarted, err := newTestManagerWithPromptMode(home, workspace, "helper-acp", "steering_prompt_required")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	recovered, err := restarted.PromptEvents(context.Background(), started.RunID, 0, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != RunCompleted {
		t.Fatalf("recovered status = %s", recovered.Status)
	}
	var recoveredChunk Event
	for _, event := range recovered.Events {
		if event.Type == "agent_message_chunk" {
			recoveredChunk = event
			break
		}
	}
	if len(recoveredChunk.Update) != 0 {
		t.Fatalf("recovered transcript payload leaked: %s", recoveredChunk.Update)
	}
	if eventJSONField(t, recoveredChunk, "update_omitted") != true {
		t.Fatalf("recovered update_omitted = %#v", eventJSONField(t, recoveredChunk, "update_omitted"))
	}
	if eventJSONField(t, recoveredChunk, "update_sha256") != wantDigest {
		t.Fatalf("recovered digest = %#v want %s", eventJSONField(t, recoveredChunk, "update_sha256"), wantDigest)
	}
}

func writeAuditFixture(t *testing.T, root, runID string, meta map[string]any, events []map[string]any) {
	t.Helper()
	runDir := filepath.Join(root, "runs", runID)
	metaBytes, _ := json.Marshal(meta)
	if err := atomicfile.Write(filepath.Join(runDir, "meta.json"), metaBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		seq := uint64(event["seq"].(int))
		data, _ := json.Marshal(event)
		path := filepath.Join(runDir, "events", fmt.Sprintf("%020d.json", seq))
		if err := atomicfile.Write(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoveredAuditNonterminalRunBecomesInterruptedWithoutDuplicateSeq(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	agent := "helper"
	store, err := newSessionStore(home, agent)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	session := SessionRecord{
		SchemaVersion: sessionSchemaVersion, ID: "acps_audit_crash", Agent: agent,
		RemoteSessionID: "remote-audit-crash", CWD: workspace, Status: SessionRunning,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	updateDigest := sha256Hex([]byte("sensitive update bytes"))
	writeAuditFixture(t, sessionStoreRoot(home, agent), "acpr_audit_crash",
		map[string]any{
			"schema_version": 1, "run_id": "acpr_audit_crash", "session_id": session.ID,
			"status": "running", "started_at": now, "updated_at": now,
		},
		[]map[string]any{{
			"schema_version": 1, "seq": 1, "type": "tool_call_update",
			"session_id": session.ID, "run_id": "acpr_audit_crash", "created_at": now,
			"tool_call_id": "tool-crash", "update_sha256": updateDigest, "original_update_bytes": 22,
		}},
	)

	manager, err := newTestManagerWithPromptMode(home, workspace, agent, "steering_prompt_required")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()
	recovered, err := manager.PromptEvents(context.Background(), "acpr_audit_crash", 0, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != RunInterrupted || recovered.StopReason != "agentdock_restart" {
		t.Fatalf("recovered run = %#v", recovered)
	}
	if len(recovered.Events) != 2 || recovered.Events[0].Seq != 1 || recovered.Events[1].Seq != 2 {
		t.Fatalf("recovered sequence = %#v", recovered.Events)
	}
	if recovered.Events[1].Type != "interrupted" {
		t.Fatalf("terminal event = %#v", recovered.Events[1])
	}
	if eventJSONField(t, recovered.Events[0], "tool_call_id") != "tool-crash" ||
		eventJSONField(t, recovered.Events[0], "update_omitted") != true ||
		eventJSONField(t, recovered.Events[0], "update_sha256") != updateDigest {
		t.Fatalf("recovered audit event = %#v", recovered.Events[0])
	}
}

func TestRecoveredAuditTerminalEventRepairsStaleRunMetadata(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	agent := "helper"
	store, err := newSessionStore(home, agent)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	session := SessionRecord{
		SchemaVersion: sessionSchemaVersion, ID: "acps_audit_terminal", Agent: agent,
		RemoteSessionID: "remote-audit-terminal", CWD: workspace, Status: SessionReady,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	writeAuditFixture(t, sessionStoreRoot(home, agent), "acpr_audit_terminal",
		map[string]any{
			"schema_version": 1, "run_id": "acpr_audit_terminal", "session_id": session.ID,
			"status": "running", "started_at": now, "updated_at": now,
		},
		[]map[string]any{{
			"schema_version": 1, "seq": 1, "type": "completed",
			"session_id": session.ID, "run_id": "acpr_audit_terminal",
			"created_at": now.Add(time.Second), "stop_reason": "end_turn",
		}},
	)

	manager, err := newTestManagerWithPromptMode(home, workspace, "helper-acp", "steering_prompt_required")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()
	recovered, err := manager.PromptEvents(context.Background(), "acpr_audit_terminal", 0, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != RunCompleted || recovered.StopReason != "end_turn" {
		t.Fatalf("stale metadata recovery = %#v", recovered)
	}
	if len(recovered.Events) != 1 || recovered.Events[0].Seq != 1 || recovered.Events[0].Type != "completed" {
		t.Fatalf("terminal recovery duplicated or changed sequence: %#v", recovered.Events)
	}
}

func TestRunJournalPersistentEventBoundPreservesCursorSemantics(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	agent := "helper"
	store, err := newSessionStore(home, agent)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	session := SessionRecord{
		SchemaVersion: sessionSchemaVersion, ID: "acps_audit_bound", Agent: agent,
		RemoteSessionID: "remote-audit-bound", CWD: workspace, Status: SessionReady,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	events := make([]map[string]any, 0, 140)
	for seq := 1; seq <= 140; seq++ {
		events = append(events, map[string]any{
			"schema_version": 1, "seq": seq, "type": "tool_call_update",
			"session_id": session.ID, "run_id": "acpr_audit_bound",
			"created_at":    now.Add(time.Duration(seq) * time.Millisecond),
			"tool_call_id":  fmt.Sprintf("tool-%03d", seq),
			"update_sha256": strings.Repeat("a", 64), "original_update_bytes": 100,
		})
	}
	ended := now.Add(time.Second)
	writeAuditFixture(t, sessionStoreRoot(home, agent), "acpr_audit_bound",
		map[string]any{
			"schema_version": 1, "run_id": "acpr_audit_bound", "session_id": session.ID,
			"status": "completed", "started_at": now, "updated_at": ended, "ended_at": ended,
			"stop_reason": "end_turn",
		}, events)

	manager, err := newTestManagerWithPromptMode(home, workspace, agent, "steering_prompt_required")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()
	page, err := manager.PromptEvents(context.Background(), "acpr_audit_bound", 0, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 128 || page.FirstSeq != 13 || page.LatestSeq != 140 ||
		page.DroppedCount != 12 || !page.Truncated || page.NextSeq != 140 {
		t.Fatalf("persistent bounded page = %#v", page)
	}
}

func TestRunJournalRunBoundPrunesOldestSettledRuns(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	agent := "helper"
	store, err := newSessionStore(home, agent)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	session := SessionRecord{
		SchemaVersion: sessionSchemaVersion, ID: "acps_audit_runs", Agent: agent,
		RemoteSessionID: "remote-audit-runs", CWD: workspace, Status: SessionReady,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	root := sessionStoreRoot(home, agent)
	for index := 0; index < 33; index++ {
		runID := fmt.Sprintf("acpr_audit_%02d", index)
		at := now.Add(time.Duration(index) * time.Minute)
		writeAuditFixture(t, root, runID, map[string]any{
			"schema_version": 1, "run_id": runID, "session_id": session.ID,
			"status": "completed", "started_at": at, "updated_at": at, "ended_at": at,
			"stop_reason": "end_turn",
		}, nil)
	}
	manager, err := newTestManagerWithPromptMode(home, workspace, agent, "steering_prompt_required")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()
	entries, err := os.ReadDir(filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 32 {
		t.Fatalf("persisted run directories = %d, want 32", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "acpr_audit_00")); !os.IsNotExist(err) {
		t.Fatalf("oldest settled run was not pruned: err=%v", err)
	}
}

func TestRunJournalSessionDeleteRemovesAuditState(t *testing.T) {
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
	started, err := manager.StartPrompt(context.Background(), created.Session.ID, "audit before delete")
	if err != nil {
		t.Fatal(err)
	}
	var permission Interaction
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending := manager.ListInteractions(created.Session.ID, true)
		if len(pending) > 0 {
			permission = pending[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if permission.ID == "" {
		t.Fatal("permission did not become pending before session delete")
	}
	if _, err := manager.RespondInteraction(permission.ID, "allow-once", false); err != nil {
		t.Fatal(err)
	}
	if result := waitForSettledRun(t, manager, started.RunID); result.Status != RunCompleted {
		t.Fatalf("run before session delete = %s", result.Status)
	}
	runDir := manager.runJournal.runDir(started.RunID)
	if _, err := os.Stat(runDir); err != nil {
		t.Fatalf("audit run missing before delete: %v", err)
	}
	if err := manager.DeleteSession(context.Background(), created.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("audit run remained after session delete: %v", err)
	}
}
