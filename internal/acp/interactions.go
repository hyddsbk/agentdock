package acp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	maxPermissionOptions             = 32
	maxPermissionToolCallBytes       = 256 << 10
	maxPendingInteractions           = 32
	maxPendingInteractionsPerSession = 8
	maxRetainedInteractions          = 256
	targetRetainedInteractions       = 192
	interactionRetentionAge          = time.Hour
)

func (m *Manager) ListInteractions(sessionID string, pendingOnly bool) []Interaction {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Interaction, 0)
	for _, interaction := range m.interactions {
		if interaction.preparing {
			continue
		}
		if sessionID != "" && interaction.SessionID != sessionID {
			continue
		}
		if pendingOnly && interaction.Status != InteractionPending {
			continue
		}
		result = append(result, interaction.public())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (m *Manager) InspectInteraction(id string) (Interaction, error) {
	m.mu.RLock()
	interaction := m.interactions[id]
	if interaction == nil || interaction.preparing {
		m.mu.RUnlock()
		return Interaction{}, newError("ACP_INTERACTION_NOT_FOUND", "ACP interaction was not found", false, map[string]any{"interaction_id": id}, nil)
	}
	result := interaction.public()
	m.mu.RUnlock()
	return result, nil
}

func (m *Manager) RespondInteraction(id, optionID string, cancelled bool) (Interaction, error) {
	m.mu.Lock()
	interaction := m.interactions[id]
	if interaction == nil || interaction.preparing {
		m.mu.Unlock()
		return Interaction{}, newError("ACP_INTERACTION_NOT_FOUND", "ACP interaction was not found", false, map[string]any{"interaction_id": id}, nil)
	}
	if interaction.Status != InteractionPending {
		result := interaction.public()
		m.mu.Unlock()
		return result, newError("ACP_INTERACTION_SETTLED", "ACP interaction is no longer pending", false, map[string]any{"interaction_id": id, "status": interaction.Status}, nil)
	}
	if !cancelled {
		valid := false
		for _, option := range interaction.Options {
			if option.OptionID == optionID {
				valid = true
				break
			}
		}
		if !valid {
			m.mu.Unlock()
			return Interaction{}, newError("ACP_PERMISSION_OPTION_INVALID", "permission option is not offered by the agent policy", false, map[string]any{"interaction_id": id, "option_id": optionID}, nil)
		}
	}
	selectedOptionID := ""
	nextApprovalStatus := ApprovalCancelled
	if !cancelled {
		selectedOptionID = optionID
		nextApprovalStatus = ApprovalDecisionRecorded
	}
	if err := m.persistApprovalStatusLocked(interaction, nextApprovalStatus, selectedOptionID); err != nil {
		m.mu.Unlock()
		return Interaction{}, err
	}
	response := interactionResponse{optionID: optionID, cancelled: cancelled}
	select {
	case interaction.respond <- response:
		if cancelled {
			interaction.Status = InteractionCancelled
		} else {
			interaction.Status = InteractionResponded
		}
		result := interaction.public()
		finalStatus := ApprovalCancelled
		if !cancelled {
			finalStatus = ApprovalResponded
		}
		persistErr := m.persistApprovalStatusLocked(interaction, finalStatus, selectedOptionID)
		m.mu.Unlock()
		if persistErr != nil {
			return result, persistErr
		}
		return result, nil
	default:
		result := interaction.public()
		m.mu.Unlock()
		return result, newError("ACP_INTERACTION_SETTLED", "ACP interaction response channel is already settled", false, map[string]any{"interaction_id": id}, nil)
	}
}

func (m *Manager) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "session/request_permission":
		result, err := m.handlePermission(ctx, params)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		return result, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	}
}

func (m *Manager) handlePermission(ctx context.Context, params json.RawMessage) (any, error) {
	var request struct {
		SessionID string             `json:"sessionId"`
		ToolCall  map[string]any     `json:"toolCall"`
		Options   []PermissionOption `json:"options"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, newError("ACP_PERMISSION_INVALID", "decode ACP permission request", false, nil, err)
	}
	m.mu.RLock()
	localID := m.remoteToLocal[request.SessionID]
	m.mu.RUnlock()
	if localID == "" {
		return cancelledPermissionOutcome(), nil
	}
	options := permittedPermissionOptions(request.Options)
	if len(options) == 0 {
		return cancelledPermissionOutcome(), nil
	}
	toolCallJSON, err := json.Marshal(request.ToolCall)
	if err != nil || len(toolCallJSON) > maxPermissionToolCallBytes {
		return cancelledPermissionOutcome(), nil
	}
	requestSHA256, err := permissionRequestSHA256(request.SessionID, request.ToolCall, options)
	if err != nil {
		return nil, newError("ACP_PERMISSION_INVALID", "hash ACP permission request", false, nil, err)
	}
	id, err := newID("acpi")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	interaction := &Interaction{
		ID:            id,
		SessionID:     localID,
		Kind:          "permission",
		Status:        InteractionPending,
		ToolCall:      request.ToolCall,
		Options:       options,
		CreatedAt:     now,
		ExpiresAt:     now.Add(m.opts.InteractionTimeout),
		requestSHA256: requestSHA256,
		preparing:     true,
		respond:       make(chan interactionResponse, 1),
	}
	m.mu.Lock()
	pendingTotal, pendingSession := 0, 0
	for _, existing := range m.interactions {
		if existing.Status != InteractionPending {
			continue
		}
		pendingTotal++
		if existing.SessionID == localID {
			pendingSession++
		}
	}
	if pendingTotal >= maxPendingInteractions || pendingSession >= maxPendingInteractionsPerSession {
		m.mu.Unlock()
		return cancelledPermissionOutcome(), nil
	}
	if err := m.persistPendingApprovalLocked(localID, request.SessionID, interaction); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.interactions[id] = interaction
	runID := m.activeRunBySession[localID]
	run := m.runs[runID]
	m.pruneInteractionsLocked(now)
	publicInteraction := interaction.public()
	m.mu.Unlock()
	if run != nil {
		visible, _ := json.Marshal(publicInteraction)
		toolCallID, _ := interaction.ToolCall["toolCallId"].(string)
		if err := run.appendEvent(Event{
			Type:       "permission_request",
			ToolCallID: boundedAuditIdentity(toolCallID),
			Update:     visible,
		}); err != nil {
			m.mu.Lock()
			if interaction.Status == InteractionPending {
				_ = m.persistApprovalStatusLocked(interaction, ApprovalCancelled, "")
				interaction.Status = InteractionCancelled
			}
			delete(m.interactions, interaction.ID)
			m.mu.Unlock()
			return nil, newError(
				"ACP_EVENT_JOURNAL_FAILED",
				"persist ACP permission audit event",
				true,
				map[string]any{"session_id": localID, "run_id": run.ID, "interaction_id": interaction.ID},
				err,
			)
		}
	}
	m.mu.Lock()
	if !m.closed && interaction.Status == InteractionPending {
		interaction.preparing = false
	}
	m.mu.Unlock()

	timer := time.NewTimer(m.opts.InteractionTimeout)
	defer timer.Stop()
	select {
	case response := <-interaction.respond:
		return permissionResponseOutcome(response), nil
	case <-timer.C:
		if response, ok := m.settleInteractionWait(interaction, InteractionExpired); ok {
			return permissionResponseOutcome(response), nil
		}
		return cancelledPermissionOutcome(), nil
	case <-ctx.Done():
		approvalStatus := ApprovalCancelled
		if m.permissionWaitInterrupted() {
			approvalStatus = ApprovalInterrupted
		}
		if response, ok := m.settleInteractionWaitWithApproval(interaction, InteractionCancelled, approvalStatus); ok {
			return permissionResponseOutcome(response), nil
		}
		return cancelledPermissionOutcome(), nil
	case <-m.closedCh:
		if response, ok := m.settleInteractionWaitWithApproval(interaction, InteractionExpired, ApprovalInterrupted); ok {
			return permissionResponseOutcome(response), nil
		}
		return cancelledPermissionOutcome(), nil
	}
}

func permittedPermissionOptions(options []PermissionOption) []PermissionOption {
	capacity := len(options)
	if capacity > maxPermissionOptions {
		capacity = maxPermissionOptions
	}
	result := make([]PermissionOption, 0, capacity)
	seen := make(map[string]struct{}, capacity)
	for _, option := range options {
		if strings.TrimSpace(option.OptionID) == "" {
			continue
		}
		if _, exists := seen[option.OptionID]; exists {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(option.Kind))
		if strings.Contains(kind, "always") {
			continue
		}
		seen[option.OptionID] = struct{}{}
		result = append(result, option)
		if len(result) == maxPermissionOptions {
			break
		}
	}
	return result
}

func cancelledPermissionOutcome() map[string]any {
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

func permissionResponseOutcome(response interactionResponse) map[string]any {
	if response.cancelled {
		return cancelledPermissionOutcome()
	}
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": response.optionID}}
}

func permissionRequestSHA256(remoteSessionID string, toolCall map[string]any, options []PermissionOption) (string, error) {
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
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:]), nil
}

func approvalMetadata(toolCall map[string]any) (toolCallID, title, kind string) {
	read := func(key string) string {
		value, _ := toolCall[key].(string)
		value = strings.TrimSpace(value)
		if len([]byte(value)) > maxApprovalMetadataBytes {
			value = truncateUTF8(value, maxApprovalMetadataBytes)
		}
		return value
	}
	return read("toolCallId"), read("title"), read("kind")
}

func (m *Manager) persistPendingApprovalLocked(localSessionID, remoteSessionID string, interaction *Interaction) error {
	record, exists := m.sessions[localSessionID]
	if !exists {
		return newError("ACP_SESSION_NOT_FOUND", "ACP session was not found while persisting permission", false, map[string]any{"session_id": localSessionID}, nil)
	}
	if record.RemoteSessionID != remoteSessionID {
		return newError("ACP_SESSION_STATE_INVALID", "ACP permission remote session does not match persisted session", false, map[string]any{"session_id": localSessionID}, nil)
	}
	for _, existing := range record.Approvals {
		if existing.ApprovalID == interaction.ID {
			return newError("ACP_PERMISSION_STATE_INVALID", "duplicate durable approval id", false, map[string]any{"interaction_id": interaction.ID}, nil)
		}
	}
	if len(record.Approvals) >= maxPersistedApprovalsPerSession {
		pruned := make([]ApprovalEnvelope, 0, len(record.Approvals))
		for _, approval := range record.Approvals {
			if approval.Status == ApprovalPending || approval.Status == ApprovalDecisionRecorded {
				pruned = append(pruned, approval)
			}
		}
		for index := len(record.Approvals) - 1; index >= 0 && len(pruned) < maxPersistedApprovalsPerSession-1; index-- {
			approval := record.Approvals[index]
			if approval.Status == ApprovalPending || approval.Status == ApprovalDecisionRecorded {
				continue
			}
			pruned = append(pruned, approval)
		}
		for left, right := 0, len(pruned)-1; left < right; left, right = left+1, right-1 {
			pruned[left], pruned[right] = pruned[right], pruned[left]
		}
		record.Approvals = pruned
	}
	if len(record.Approvals) >= maxPersistedApprovalsPerSession {
		return newError("ACP_PERMISSION_STATE_LIMIT", "ACP durable approval history is full of unresolved entries", false, map[string]any{"session_id": localSessionID}, nil)
	}
	toolCallID, title, kind := approvalMetadata(interaction.ToolCall)
	envelope := ApprovalEnvelope{
		ApprovalID:    interaction.ID,
		RequestSHA256: interaction.requestSHA256,
		ToolCallID:    toolCallID,
		Title:         title,
		Kind:          kind,
		Status:        ApprovalPending,
		CreatedAt:     interaction.CreatedAt,
		UpdatedAt:     interaction.CreatedAt,
		ExpiresAt:     interaction.ExpiresAt,
	}
	record.Approvals = append(record.Approvals, envelope)
	if err := m.store.Save(record); err != nil {
		return newError("ACP_PERMISSION_STATE_FAILED", "persist ACP permission state", true, map[string]any{"session_id": localSessionID}, err)
	}
	m.sessions[localSessionID] = record
	return nil
}

func (m *Manager) persistApprovalStatusLocked(interaction *Interaction, status ApprovalStatus, selectedOptionID string) error {
	if interaction == nil {
		return newError("ACP_PERMISSION_STATE_INVALID", "ACP interaction is nil", false, nil, nil)
	}
	record, exists := m.sessions[interaction.SessionID]
	if !exists {
		return newError("ACP_SESSION_NOT_FOUND", "ACP session was not found while updating permission", false, map[string]any{"session_id": interaction.SessionID}, nil)
	}
	record.Approvals = append([]ApprovalEnvelope(nil), record.Approvals...)
	index := -1
	for i := range record.Approvals {
		if record.Approvals[i].ApprovalID == interaction.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return newError("ACP_PERMISSION_STATE_INVALID", "durable ACP approval was not found", false, map[string]any{"interaction_id": interaction.ID}, nil)
	}
	approval := record.Approvals[index]
	if approval.RequestSHA256 != interaction.requestSHA256 {
		return newError("ACP_PERMISSION_DIGEST_MISMATCH", "ACP permission digest does not match durable approval", false, map[string]any{"interaction_id": interaction.ID}, nil)
	}
	if approval.Status == ApprovalInterrupted {
		return newError("ACP_PERMISSION_INTERRUPTED", "ACP permission is no longer attached to a live request", false, map[string]any{"interaction_id": interaction.ID}, nil)
	}
	switch status {
	case ApprovalDecisionRecorded:
		if approval.Status != ApprovalPending {
			return newError("ACP_PERMISSION_STATE_INVALID", "ACP approval is not pending", false, map[string]any{"interaction_id": interaction.ID, "status": approval.Status}, nil)
		}
	case ApprovalResponded:
		if approval.Status != ApprovalDecisionRecorded {
			return newError("ACP_PERMISSION_STATE_INVALID", "ACP approval decision is not recorded", false, map[string]any{"interaction_id": interaction.ID, "status": approval.Status}, nil)
		}
	case ApprovalCancelled, ApprovalExpired, ApprovalInterrupted:
		if approval.Status != ApprovalPending && approval.Status != ApprovalDecisionRecorded {
			return nil
		}
	default:
		return newError("ACP_PERMISSION_STATE_INVALID", "invalid ACP approval transition", false, map[string]any{"interaction_id": interaction.ID, "status": status}, nil)
	}
	approval.Status = status
	if selectedOptionID != "" || status == ApprovalCancelled {
		approval.SelectedOptionID = selectedOptionID
	}
	approval.UpdatedAt = time.Now().UTC()
	record.Approvals[index] = approval
	if err := m.store.Save(record); err != nil {
		return newError("ACP_PERMISSION_STATE_FAILED", "persist ACP permission transition", true, map[string]any{"interaction_id": interaction.ID}, err)
	}
	m.sessions[interaction.SessionID] = record
	return nil
}

func (m *Manager) settleInteractionWait(interaction *Interaction, terminal InteractionStatus) (interactionResponse, bool) {
	approvalStatus := ApprovalCancelled
	if terminal == InteractionExpired {
		approvalStatus = ApprovalExpired
	}
	return m.settleInteractionWaitWithApproval(interaction, terminal, approvalStatus)
}

func (m *Manager) settleInteractionWaitWithApproval(interaction *Interaction, terminal InteractionStatus, approvalStatus ApprovalStatus) (interactionResponse, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case response := <-interaction.respond:
		return response, true
	default:
	}
	if interaction.Status == InteractionPending {
		interaction.Status = terminal
		_ = m.persistApprovalStatusLocked(interaction, approvalStatus, "")
	}
	return interactionResponse{}, false
}

func (m *Manager) permissionWaitInterrupted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return true
	}
	process := m.process
	if process == nil || process.connection == nil {
		return false
	}
	select {
	case <-process.connection.Closed():
		return true
	default:
		return false
	}
}

func (m *Manager) cancelPendingInteractions(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, interaction := range m.interactions {
		if interaction.SessionID != sessionID || interaction.Status != InteractionPending {
			continue
		}
		interaction.Status = InteractionCancelled
		_ = m.persistApprovalStatusLocked(interaction, ApprovalCancelled, "")
		select {
		case interaction.respond <- interactionResponse{cancelled: true}:
		default:
		}
	}
}

func (m *Manager) pruneInteractionsLocked(now time.Time) {
	if len(m.interactions) <= maxRetainedInteractions {
		return
	}
	settled := make([]*Interaction, 0, len(m.interactions))
	for _, interaction := range m.interactions {
		if interaction.Status == InteractionPending {
			continue
		}
		settled = append(settled, interaction)
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i].CreatedAt.Before(settled[j].CreatedAt) })
	for _, agedOnly := range []bool{true, false} {
		for _, interaction := range settled {
			if len(m.interactions) <= targetRetainedInteractions {
				return
			}
			if agedOnly && now.Sub(interaction.CreatedAt) < interactionRetentionAge {
				continue
			}
			delete(m.interactions, interaction.ID)
		}
	}
}
