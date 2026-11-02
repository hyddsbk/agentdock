package acp

import "encoding/json"

func (m *Manager) handleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var notification struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(params, &notification); err != nil || notification.SessionID == "" {
		return
	}
	m.mu.RLock()
	localID := m.remoteToLocal[notification.SessionID]
	if localID == "" {
		m.mu.RUnlock()
		return
	}
	runID := m.activeRunBySession[localID]
	run := m.runs[runID]
	m.mu.RUnlock()
	if run == nil {
		return
	}
	eventType := "session_update"
	var updateType struct {
		SessionUpdate string `json:"sessionUpdate"`
		ToolCallID    string `json:"toolCallId,omitempty"`
		MessageID     string `json:"messageId,omitempty"`
	}
	if json.Unmarshal(notification.Update, &updateType) == nil && updateType.SessionUpdate != "" {
		eventType = updateType.SessionUpdate
	}
	// agent_thought_chunk 属于模型私有推理过程，不是面向调用方的会话输出。
	// 在事件进入持久的 run ring 之前丢弃，避免后续 MCP/HTTP 客户端读取到内部思考内容。
	if eventType == "agent_thought_chunk" {
		return
	}
	if err := run.appendSessionUpdate(Event{
		Type:       eventType,
		ToolCallID: boundedAuditIdentity(updateType.ToolCallID),
		MessageID:  boundedAuditIdentity(updateType.MessageID),
		Update:     append(json.RawMessage(nil), notification.Update...),
	}); err != nil {
		if run.cancel != nil {
			run.cancel()
		}
		m.finishRun(run, RunFailed, "", newError(
			"ACP_EVENT_JOURNAL_FAILED",
			"persist ACP session update audit event",
			true,
			map[string]any{"session_id": run.SessionID, "run_id": run.ID},
			err,
		))
	}
}
