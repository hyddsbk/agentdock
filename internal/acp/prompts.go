package acp

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const (
	maxRetainedRuns             = 256
	targetRetainedRuns          = 192
	maxConversationTurns        = 8
	maxConversationMessageBytes = 8 << 10
)

type PromptStartResult struct {
	RunID     string    `json:"run_id"`
	SessionID string    `json:"session_id"`
	Status    RunStatus `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

type PromptEventsResult struct {
	RunID        string     `json:"run_id"`
	SessionID    string     `json:"session_id"`
	Status       RunStatus  `json:"status"`
	Events       []Event    `json:"events"`
	NextSeq      uint64     `json:"next_seq"`
	FirstSeq     uint64     `json:"first_seq"`
	LatestSeq    uint64     `json:"latest_seq"`
	DroppedCount uint64     `json:"dropped_count"`
	HasMore      bool       `json:"has_more"`
	Truncated    bool       `json:"truncated"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	StopReason   string     `json:"stop_reason,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	Message      string     `json:"message,omitempty"`
}

type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (m *Manager) StartPrompt(ctx context.Context, sessionID, text string) (PromptStartResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return PromptStartResult{}, newError("ACP_PROMPT_INVALID", "ACP prompt text is required", false, nil, nil)
	}
	if len([]byte(text)) > 256<<10 {
		return PromptStartResult{}, newError("ACP_PROMPT_TOO_LARGE", "ACP prompt exceeds 256 KiB", false, map[string]any{"bytes": len([]byte(text))}, nil)
	}
	if _, err := m.LoadSession(ctx, sessionID); err != nil {
		return PromptStartResult{}, err
	}
	endOperation, err := m.beginSessionOperation(sessionID)
	if err != nil {
		return PromptStartResult{}, err
	}
	defer endOperation()
	select {
	case m.runSlots <- struct{}{}:
	default:
		return PromptStartResult{}, newError("ACP_BUSY", "ACP concurrent prompt limit reached", true, map[string]any{"limit": cap(m.runSlots)}, nil)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		<-m.runSlots
		return PromptStartResult{}, newError("ACP_MANAGER_CLOSED", "ACP manager is closed", false, nil, nil)
	}
	if existing := m.activeRunBySession[sessionID]; existing != "" {
		m.mu.Unlock()
		<-m.runSlots
		return PromptStartResult{}, newError("ACP_SESSION_BUSY", "ACP session already has an active prompt", true, map[string]any{"session_id": sessionID, "run_id": existing}, nil)
	}
	runID, err := newID("acpr")
	if err != nil {
		m.mu.Unlock()
		<-m.runSlots
		return PromptStartResult{}, err
	}
	run := newRun(runID, sessionID)
	// Widget 对话只保留当前进程内的最小 user/assistant 文本，不把 prompt 全文
	// 写入 session 持久化文件；AgentDock 重启后由远端 ACP session 继续持有其历史。
	run.userText = truncateUTF8(text, maxConversationMessageBytes)
	runCtx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	m.pruneRunsLocked(time.Now().UTC())
	m.runs[runID] = run
	m.activeRunBySession[sessionID] = runID
	record, exists := m.sessions[sessionID]
	if !exists {
		delete(m.activeRunBySession, sessionID)
		delete(m.runs, runID)
		m.mu.Unlock()
		cancel()
		<-m.runSlots
		return PromptStartResult{}, newError("ACP_SESSION_NOT_FOUND", "ACP session was not found", false, map[string]any{"session_id": sessionID}, nil)
	}
	record.Status = SessionRunning
	record.UpdatedAt = time.Now().UTC()
	m.sessions[sessionID] = record
	m.mu.Unlock()

	if err := m.store.Save(record); err != nil {
		m.finishRun(run, RunFailed, "", err)
		return PromptStartResult{}, err
	}
	if err := m.runJournal.Start(run); err != nil {
		journalErr := newError(
			"ACP_EVENT_JOURNAL_FAILED",
			"initialize ACP audit journal",
			true,
			map[string]any{"session_id": sessionID, "run_id": run.ID},
			err,
		)
		m.mu.Lock()
		delete(m.activeRunBySession, sessionID)
		delete(m.runs, run.ID)
		if current, ok := m.sessions[sessionID]; ok {
			current.Status = SessionReady
			current.LastStopReason = ""
			current.UpdatedAt = time.Now().UTC()
			m.sessions[sessionID] = current
			record = current
		}
		m.mu.Unlock()
		_ = m.store.Save(record)
		cancel()
		select {
		case <-m.runSlots:
		default:
		}
		return PromptStartResult{}, journalErr
	}
	run.eventsMu.Lock()
	run.persistEvent = m.runJournal.Append
	run.eventsMu.Unlock()
	go m.runPrompt(runCtx, run, record, text)
	return PromptStartResult{RunID: run.ID, SessionID: sessionID, Status: RunRunning, StartedAt: run.StartedAt}, nil
}

func (m *Manager) SessionMessages(sessionID string) ([]ConversationMessage, error) {
	if _, err := m.session(sessionID); err != nil {
		return nil, err
	}

	// 先在 manager 锁下只复制 run 指针，再分别读取 run。finishRun 的锁顺序是
	// run -> manager，这里不能反向同时持有两把锁。
	m.mu.RLock()
	runs := make([]*Run, 0)
	for _, run := range m.runs {
		if run != nil && run.SessionID == sessionID {
			runs = append(runs, run)
		}
	}
	m.mu.RUnlock()
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
	if len(runs) > maxConversationTurns {
		runs = runs[len(runs)-maxConversationTurns:]
	}

	messages := make([]ConversationMessage, 0, len(runs)*2)
	for _, run := range runs {
		run.eventsMu.Lock()
		userText := strings.TrimSpace(run.userText)
		status := run.Status
		var assistant strings.Builder
		for _, event := range run.events {
			if status != RunCompleted || event.Type != "agent_message_chunk" {
				continue
			}
			remaining := maxConversationMessageBytes - assistant.Len()
			if remaining <= 0 {
				break
			}
			assistant.WriteString(truncateUTF8(assistantChunkText(event.Update), remaining))
		}
		assistantText := strings.TrimSpace(assistant.String())
		run.eventsMu.Unlock()

		if userText != "" {
			messages = append(messages, ConversationMessage{Role: "user", Content: userText})
		}
		if assistantText != "" {
			messages = append(messages, ConversationMessage{Role: "assistant", Content: assistantText})
		}
	}
	return messages, nil
}

func (m *Manager) PromptEvents(ctx context.Context, runID string, after uint64, limit int, wait time.Duration) (PromptEventsResult, error) {
	run, err := m.run(runID)
	if err != nil {
		return PromptEventsResult{}, err
	}
	result, err := run.promptEventsSnapshot(after, limit)
	if err != nil {
		return PromptEventsResult{}, err
	}
	if len(result.Events) == 0 && result.Status == RunRunning && wait > 0 {
		if err := m.waitForPromptEvents(ctx, run, after, wait); err != nil {
			return PromptEventsResult{}, err
		}
		result, err = run.promptEventsSnapshot(after, limit)
		if err != nil {
			return PromptEventsResult{}, err
		}
	}
	if result.Status != RunRunning {
		select {
		case <-run.finalized:
		case <-ctx.Done():
			return PromptEventsResult{}, newError("ACP_EVENTS_CANCELLED", "ACP event read was cancelled while the run finalized", true, map[string]any{"run_id": run.ID}, ctx.Err())
		}
		return run.promptEventsSnapshot(after, limit)
	}
	return result, nil
}

func (r *Run) promptEventsSnapshot(after uint64, limit int) (PromptEventsResult, error) {
	r.eventsMu.Lock()
	defer r.eventsMu.Unlock()
	page, err := r.eventsAfterLocked(after, limit)
	if err != nil {
		return PromptEventsResult{}, err
	}
	result := PromptEventsResult{
		RunID:        r.ID,
		SessionID:    r.SessionID,
		Status:       r.Status,
		Events:       page.Events,
		NextSeq:      page.NextSeq,
		FirstSeq:     page.FirstSeq,
		LatestSeq:    page.LatestSeq,
		DroppedCount: page.DroppedCount,
		HasMore:      page.HasMore,
		Truncated:    page.Truncated,
		StartedAt:    r.StartedAt,
		EndedAt:      r.EndedAt,
		StopReason:   r.StopReason,
	}
	if r.Err != nil {
		result.ErrorCode = errorCode(r.Err)
		result.Message = r.Err.Error()
	}
	return result, nil
}

func (m *Manager) waitForPromptEvents(ctx context.Context, run *Run, after uint64, wait time.Duration) error {
	if wait > 25*time.Second {
		wait = 25 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		// Drain a stale wake-up before taking a fresh, single-lock snapshot. The
		// second check closes the race where an event arrives between drain and wait.
		select {
		case <-run.notify:
		default:
		}
		result, err := run.promptEventsSnapshot(after, 1)
		if err != nil {
			return err
		}
		if len(result.Events) > 0 || result.Status != RunRunning {
			return nil
		}
		select {
		case <-run.notify:
			continue
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return newError("ACP_EVENTS_CANCELLED", "ACP event wait was cancelled", true, map[string]any{"run_id": run.ID}, ctx.Err())
		case <-m.closedCh:
			return newError("ACP_MANAGER_CLOSED", "ACP manager is closed", true, nil, nil)
		}
	}
}

func (m *Manager) CancelPrompt(_ context.Context, sessionID, runID string) error {
	m.mu.RLock()
	explicitRunID := runID != ""
	if runID == "" {
		runID = m.activeRunBySession[sessionID]
	}
	run := m.runs[runID]
	var record SessionRecord
	if run != nil {
		if sessionID != "" && sessionID != run.SessionID {
			m.mu.RUnlock()
			return newError("ACP_CANCEL_TARGET_MISMATCH", "ACP run does not belong to the supplied session", false, map[string]any{"session_id": sessionID, "run_id": runID, "run_session_id": run.SessionID}, nil)
		}
		sessionID = run.SessionID
		record = m.sessions[sessionID]
	}
	process := m.process
	m.mu.RUnlock()
	if run == nil {
		if explicitRunID {
			return newError("ACP_RUN_NOT_FOUND", "ACP prompt run was not found", false, map[string]any{"run_id": runID}, nil)
		}
		return nil
	}
	if runStatus(run) != RunRunning {
		if explicitRunID {
			return newError("ACP_RUN_SETTLED", "ACP prompt run is no longer running", false, map[string]any{"run_id": runID}, nil)
		}
		return nil
	}
	if process != nil && record.RemoteSessionID != "" {
		_ = process.connection.Notify("session/cancel", map[string]any{"sessionId": record.RemoteSessionID})
	}
	if run.cancel != nil {
		run.cancel()
	}
	m.cancelPendingInteractions(sessionID)
	m.finishRun(run, RunCancelled, "cancelled", nil)
	return nil
}

func (m *Manager) Steer(ctx context.Context, sessionID, text string) (map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, newError("ACP_PROMPT_INVALID", "ACP steering text is required", false, nil, nil)
	}
	loaded, err := m.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	record := loaded.Session
	endOperation, err := m.beginSessionOperation(sessionID)
	if err != nil {
		return nil, err
	}
	defer endOperation()
	process, err := m.ensureProcess(ctx)
	if err != nil {
		return nil, err
	}
	if !process.supportsSteering() {
		return nil, capabilityError("_meta.steering.supported")
	}
	if requiresHostSteeringFallback(process.initialize.AgentInfo) {
		return m.startHostOwnedSteering(ctx, process, record, text, hostSteeringFallbackReason(process.initialize.AgentInfo))
	}
	var result map[string]any
	if err := process.connection.Request(ctx, "_session/steering", map[string]any{
		"sessionId": record.RemoteSessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
		"_meta":     map[string]any{"steering": map[string]any{"idleBehavior": "promptRequired"}},
	}, &result); err != nil {
		return nil, process.wrapError("steer ACP session", err)
	}
	if result["outcome"] == "promptRequired" {
		return m.startHostOwnedSteering(ctx, process, record, text, "adapter_prompt_required")
	}
	return result, nil
}

func (m *Manager) startHostOwnedSteering(ctx context.Context, process *agentProcess, record SessionRecord, text, reason string) (map[string]any, error) {
	sessionID := record.ID
	m.mu.RLock()
	cancelledRunID := m.activeRunBySession[sessionID]
	m.mu.RUnlock()
	if cancelledRunID != "" {
		if err := m.CancelPrompt(ctx, sessionID, cancelledRunID); err != nil && errorCode(err) != "ACP_RUN_SETTLED" {
			return nil, err
		}
		// Affected adapters can retain cancelled turn state in memory. A standard
		// close/load cycle resets that state while preserving the adapter-owned
		// transcript whenever the remote session already has persisted history.
		if !process.supportsLoadSession() || !process.supportsSessionCapability("close") {
			return nil, capabilityError("loadSession + sessionCapabilities.close")
		}
		if err := process.connection.Request(ctx, "session/close", map[string]any{"sessionId": record.RemoteSessionID}, nil); err != nil {
			m.markSessionInterrupted(record, "steering_reset_failed")
			return nil, process.wrapError("reset ACP session after steering cancellation", err)
		}
		var loaded sessionLifecycleResponse
		if err := process.connection.Request(ctx, "session/load", sessionActivationParams(record), &loaded); err != nil {
			wrapped := process.wrapError("reload ACP session after steering cancellation", err)
			if !isCodexNoRolloutError(process.initialize.AgentInfo, wrapped) {
				m.markSessionInterrupted(record, "steering_reload_failed")
				return nil, wrapped
			}

			// codex-acp 1.1.9 cannot reload a cancelled first turn before any
			// rollout exists. The cancelled turn has no transcript to preserve, so
			// recreate only the remote thread while keeping the AgentDock session.
			if err := process.connection.Request(ctx, "session/new", sessionCreationParams(record.CWD, record.AdditionalDirectories), &loaded); err != nil {
				m.markSessionInterrupted(record, "steering_recreate_failed")
				return nil, process.wrapError("recreate ACP session after unpersisted steering cancellation", err)
			}
			updatedRecord, rebindErr := m.rebindRemoteSession(record, loaded)
			if rebindErr != nil {
				m.markSessionInterrupted(record, "steering_rebind_failed")
				return nil, rebindErr
			}
			record = updatedRecord
		}
		if err := m.restoreSessionMode(ctx, process, record, &loaded); err != nil {
			m.markSessionInterrupted(record, "steering_mode_restore_failed")
			return nil, err
		}
		if _, err := m.markSessionReady(record, loaded); err != nil {
			return nil, err
		}
	}
	started, err := m.StartPrompt(ctx, sessionID, text)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"outcome": "startedNewTurn", "reason": reason,
		"runId": started.RunID, "sessionId": started.SessionID,
	}
	if cancelledRunID != "" {
		result["cancelledRunId"] = cancelledRunID
	}
	return result, nil
}

func (m *Manager) markSessionInterrupted(record SessionRecord, reason string) {
	now := time.Now().UTC()
	m.mu.Lock()
	current, exists := m.sessions[record.ID]
	_, terminal := m.terminalSessions[record.ID]
	if exists && !terminal {
		current.Status = SessionInterrupted
		current.LastStopReason = reason
		current.UpdatedAt = now
		m.sessions[record.ID] = current
		delete(m.loaded, record.ID)
		record = current
	}
	m.mu.Unlock()
	if exists && !terminal {
		if err := m.store.Save(record); err != nil {
			slog.Warn("persist interrupted ACP session failed", "session_id", record.ID, "reason", reason, "error", err)
		}
	}
}

func (m *Manager) runPrompt(ctx context.Context, run *Run, record SessionRecord, text string) {
	m.mu.RLock()
	process := m.process
	m.mu.RUnlock()
	if process == nil {
		m.finishRun(run, RunFailed, "", newError("ACP_CONNECTION_FAILED", "ACP process is not available", true, nil, nil))
		return
	}
	var response struct {
		StopReason string `json:"stopReason"`
	}
	err := process.connection.Request(ctx, "session/prompt", map[string]any{
		"sessionId": record.RemoteSessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}, &response)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			m.finishRun(run, RunCancelled, "cancelled", nil)
			return
		}
		status := RunFailed
		var acpErr *Error
		if errors.As(err, &acpErr) && acpErr.Code == "ACP_CONNECTION_CLOSED" {
			status = RunInterrupted
		}
		m.finishRun(run, status, "", process.wrapError("run ACP prompt", err))
		return
	}
	if process.initialize.AgentInfo.Name == "@agentclientprotocol/codex-acp" {
		if remoteMessage, falseSuccess := run.codexFalseSuccess(); falseSuccess {
			remoteErr := newError(
				"ACP_REMOTE_ERROR",
				"Codex turn failed after remote retries",
				true,
				map[string]any{"adapter": "codex-acp", "remote_message": remoteMessage},
				nil,
			)
			m.finishRun(run, RunFailed, "", process.wrapError("run ACP prompt", remoteErr))
			return
		}
	}
	m.finishRun(run, RunCompleted, response.StopReason, nil)
}

func (m *Manager) finishRun(run *Run, status RunStatus, stopReason string, err error) {
	now := time.Now().UTC()
	run.eventsMu.Lock()
	if run.Status != RunRunning {
		run.eventsMu.Unlock()
		return
	}
	run.Status = status
	run.EndedAt = &now
	run.StopReason = stopReason
	run.Err = err
	terminalEvent := Event{}
	switch {
	case err != nil:
		terminalEvent = Event{Type: "error", ErrorCode: errorCode(err), Message: err.Error()}
	case status == RunCompleted:
		terminalEvent = Event{Type: "completed", StopReason: stopReason}
	case status == RunCancelled:
		terminalEvent = Event{Type: "cancelled", StopReason: stopReason}
	case status == RunInterrupted:
		terminalEvent = Event{Type: "interrupted", StopReason: stopReason}
	}
	if terminalEvent.Type != "" {
		if journalErr := run.appendEventLocked(terminalEvent); journalErr != nil {
			status = RunFailed
			stopReason = ""
			err = newError(
				"ACP_EVENT_JOURNAL_FAILED",
				"persist ACP terminal audit event",
				true,
				map[string]any{"session_id": run.SessionID, "run_id": run.ID},
				journalErr,
			)
			run.Status = status
			run.StopReason = stopReason
			run.Err = err
		}
	}
	run.eventsMu.Unlock()
	if m.runJournal != nil {
		if journalErr := m.runJournal.Finish(run); journalErr != nil {
			slog.Warn("finalize ACP audit journal metadata failed", "session_id", run.SessionID, "run_id", run.ID, "error", journalErr)
		}
	}

	m.mu.Lock()
	delete(m.activeRunBySession, run.SessionID)
	_, terminalTransition := m.terminalSessions[run.SessionID]
	var record SessionRecord
	recordExists := false
	if !terminalTransition {
		record, recordExists = m.sessions[run.SessionID]
	}
	if !terminalTransition && recordExists {
		switch status {
		case RunInterrupted:
			record.Status = SessionInterrupted
		case RunFailed:
			record.Status = SessionError
		default:
			record.Status = SessionReady
		}
		record.LastStopReason = stopReason
		if err != nil && record.LastStopReason == "" {
			record.LastStopReason = errorCode(err)
		}
		record.UpdatedAt = now
		m.sessions[run.SessionID] = record
	}
	m.mu.Unlock()
	if !terminalTransition && recordExists {
		if saveErr := m.store.Save(record); saveErr != nil {
			slog.Warn("persist ACP session after prompt failed", "session_id", run.SessionID, "run_id", run.ID, "error", saveErr)
		}
	}
	run.finalizeOnce.Do(func() { close(run.finalized) })
	run.signalEvent()
	select {
	case <-m.runSlots:
	default:
	}
}

func runStatus(run *Run) RunStatus {
	run.eventsMu.Lock()
	defer run.eventsMu.Unlock()
	return run.Status
}

func (m *Manager) pruneRunsLocked(now time.Time) {
	if len(m.runs) <= maxRetainedRuns {
		return
	}
	for id, run := range m.runs {
		run.eventsMu.Lock()
		settled := run.Status != RunRunning
		endedAt := run.EndedAt
		run.eventsMu.Unlock()
		if settled && endedAt != nil && now.Sub(*endedAt) >= time.Hour {
			delete(m.runs, id)
		}
	}
	if len(m.runs) <= maxRetainedRuns {
		return
	}
	for id, run := range m.runs {
		run.eventsMu.Lock()
		settled := run.Status != RunRunning
		run.eventsMu.Unlock()
		if settled {
			delete(m.runs, id)
		}
		if len(m.runs) <= targetRetainedRuns {
			break
		}
	}
}
