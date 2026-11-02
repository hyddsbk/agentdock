package acp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/securepath"
)

const (
	auditJournalSchemaVersion = 1
	maxPersistedAuditRuns     = 32
	maxAuditEventsPerRun      = 128
	maxAuditBytesPerRun       = 512 << 10
	maxAuditIdentityBytes     = 4 << 10
)

type runAuditMeta struct {
	SchemaVersion int        `json:"schema_version"`
	RunID         string     `json:"run_id"`
	SessionID     string     `json:"session_id"`
	Status        RunStatus  `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	StopReason    string     `json:"stop_reason,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	MessageSHA256 string     `json:"message_sha256,omitempty"`
}

type auditEventRecord struct {
	SchemaVersion       int       `json:"schema_version"`
	Seq                 uint64    `json:"seq"`
	Type                string    `json:"type"`
	SessionID           string    `json:"session_id"`
	RunID               string    `json:"run_id"`
	ToolCallID          string    `json:"tool_call_id,omitempty"`
	MessageID           string    `json:"message_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdateSHA256        string    `json:"update_sha256,omitempty"`
	UpdateTruncated     bool      `json:"update_truncated,omitempty"`
	OriginalUpdateBytes int       `json:"original_update_bytes,omitempty"`
	StopReason          string    `json:"stop_reason,omitempty"`
	ErrorCode           string    `json:"error_code,omitempty"`
	MessageSHA256       string    `json:"message_sha256,omitempty"`
}

type runJournalStore struct {
	root string
	mu   sync.Mutex
}

func newRunJournalStore(sessionRoot string) (*runJournalStore, error) {
	if strings.TrimSpace(sessionRoot) == "" {
		return nil, errors.New("ACP run journal session root is required")
	}
	root := filepath.Join(sessionRoot, "runs")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create ACP run journal: %w", err)
	}
	if err := securepath.EnsurePrivate(root); err != nil {
		return nil, fmt.Errorf("secure ACP run journal: %w", err)
	}
	store := &runJournalStore{root: root}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.pruneRunsLocked(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list ACP run journal: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := store.pruneEventsLocked(entry.Name()); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *runJournalStore) Start(run *Run) error {
	if run == nil {
		return errors.New("ACP run journal start requires run")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := runAuditMeta{
		SchemaVersion: auditJournalSchemaVersion,
		RunID:         run.ID,
		SessionID:     run.SessionID,
		Status:        RunRunning,
		StartedAt:     run.StartedAt,
		UpdatedAt:     run.StartedAt,
	}
	if err := s.writeMetaLocked(meta); err != nil {
		return err
	}
	return s.pruneRunsLocked()
}

func (s *runJournalStore) Append(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := eventToAuditRecord(event)
	if err := validateAuditEvent(record); err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode ACP audit event: %w", err)
	}
	path := filepath.Join(s.runDir(record.RunID), "events", auditEventFilename(record.Seq))
	if err := atomicfile.Write(path, encoded, 0o600); err != nil {
		return fmt.Errorf("persist ACP audit event: %w", err)
	}
	return s.pruneEventsLocked(record.RunID)
}

func (s *runJournalStore) Finish(run *Run) error {
	if run == nil {
		return errors.New("ACP run journal finish requires run")
	}
	run.eventsMu.Lock()
	meta := runAuditMeta{
		SchemaVersion: auditJournalSchemaVersion,
		RunID:         run.ID,
		SessionID:     run.SessionID,
		Status:        run.Status,
		StartedAt:     run.StartedAt,
		EndedAt:       run.EndedAt,
		StopReason:    run.StopReason,
		ErrorCode:     errorCode(run.Err),
	}
	if run.Err == nil {
		meta.ErrorCode = ""
	} else {
		meta.MessageSHA256 = digestStringSHA256(run.Err.Error())
	}
	if run.EndedAt != nil {
		meta.UpdatedAt = *run.EndedAt
	} else {
		meta.UpdatedAt = time.Now().UTC()
	}
	run.eventsMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeMetaLocked(meta); err != nil {
		return err
	}
	return s.pruneRunsLocked()
}

func (s *runJournalStore) DeleteRun(runID string) error {
	if !validJournalIdentity(runID) {
		return errors.New("invalid ACP run journal identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.runDir(runID)); err != nil {
		return fmt.Errorf("delete ACP run journal %s: %w", runID, err)
	}
	return nil
}

func (s *runJournalStore) DeleteSession(sessionID string) error {
	if !validJournalIdentity(sessionID) {
		return errors.New("invalid ACP session journal identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("list ACP run journal for session deletion: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(entry.Name())
		if err != nil {
			return err
		}
		if meta.SessionID != sessionID {
			continue
		}
		if err := os.RemoveAll(s.runDir(meta.RunID)); err != nil {
			return fmt.Errorf("delete ACP run journal %s: %w", meta.RunID, err)
		}
	}
	return nil
}

func (s *runJournalStore) LoadRecoveredRuns() ([]*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.pruneRunsLocked(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list ACP run journal: %w", err)
	}
	result := make([]*Run, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		if err := s.pruneEventsLocked(runID); err != nil {
			return nil, err
		}
		meta, err := s.readMetaLocked(runID)
		if err != nil {
			return nil, err
		}
		records, err := s.readEventsLocked(runID)
		if err != nil {
			return nil, err
		}
		if meta.Status == RunRunning {
			if len(records) > 0 {
				if status, stopReason, errorCode, messageSHA, ok := terminalAuditState(records[len(records)-1]); ok {
					endedAt := records[len(records)-1].CreatedAt
					meta.Status = status
					meta.StopReason = stopReason
					meta.ErrorCode = errorCode
					meta.MessageSHA256 = messageSHA
					meta.EndedAt = &endedAt
					meta.UpdatedAt = endedAt
					if err := s.writeMetaLocked(meta); err != nil {
						return nil, err
					}
				}
			}
		}
		if meta.Status == RunRunning {
			nextSeq := uint64(1)
			if len(records) > 0 {
				nextSeq = records[len(records)-1].Seq + 1
			}
			now := time.Now().UTC()
			terminal := Event{
				Seq:        nextSeq,
				Type:       "interrupted",
				SessionID:  meta.SessionID,
				RunID:      meta.RunID,
				CreatedAt:  now,
				StopReason: "agentdock_restart",
			}
			audit := eventToAuditRecord(terminal)
			encoded, err := json.Marshal(audit)
			if err != nil {
				return nil, err
			}
			if err := atomicfile.Write(
				filepath.Join(s.runDir(runID), "events", auditEventFilename(nextSeq)),
				encoded, 0o600,
			); err != nil {
				return nil, fmt.Errorf("persist recovered ACP terminal audit event: %w", err)
			}
			if err := s.pruneEventsLocked(runID); err != nil {
				return nil, err
			}
			meta.Status = RunInterrupted
			meta.StopReason = "agentdock_restart"
			meta.EndedAt = &now
			meta.UpdatedAt = now
			meta.ErrorCode = ""
			meta.MessageSHA256 = ""
			if err := s.writeMetaLocked(meta); err != nil {
				return nil, err
			}
			records, err = s.readEventsLocked(runID)
			if err != nil {
				return nil, err
			}
		}
		run, err := runFromAudit(meta, records, s)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.Before(result[j].StartedAt) })
	return result, nil
}

func terminalAuditState(record auditEventRecord) (RunStatus, string, string, string, bool) {
	switch record.Type {
	case "completed":
		return RunCompleted, record.StopReason, "", "", true
	case "cancelled":
		return RunCancelled, record.StopReason, "", "", true
	case "interrupted":
		return RunInterrupted, record.StopReason, "", "", true
	case "error":
		return RunFailed, "", record.ErrorCode, record.MessageSHA256, true
	default:
		return "", "", "", "", false
	}
}

func (s *runJournalStore) runDir(runID string) string {
	return filepath.Join(s.root, runID)
}

func (s *runJournalStore) writeMetaLocked(meta runAuditMeta) error {
	if err := validateRunAuditMeta(meta); err != nil {
		return err
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode ACP run journal metadata: %w", err)
	}
	if len(encoded) > 64<<10 {
		return errors.New("ACP run journal metadata exceeds 64 KiB")
	}
	if err := atomicfile.Write(filepath.Join(s.runDir(meta.RunID), "meta.json"), encoded, 0o600); err != nil {
		return fmt.Errorf("persist ACP run journal metadata: %w", err)
	}
	return nil
}

func (s *runJournalStore) readMetaLocked(runID string) (runAuditMeta, error) {
	data, err := os.ReadFile(filepath.Join(s.runDir(runID), "meta.json"))
	if err != nil {
		return runAuditMeta{}, fmt.Errorf("read ACP run journal metadata: %w", err)
	}
	var meta runAuditMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return runAuditMeta{}, fmt.Errorf("decode ACP run journal metadata: %w", err)
	}
	if meta.RunID != runID {
		return runAuditMeta{}, fmt.Errorf("ACP run journal metadata id mismatch: %q != %q", meta.RunID, runID)
	}
	if err := validateRunAuditMeta(meta); err != nil {
		return runAuditMeta{}, err
	}
	return meta, nil
}

func (s *runJournalStore) readEventsLocked(runID string) ([]auditEventRecord, error) {
	dir := filepath.Join(s.runDir(runID), "events")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list ACP audit events: %w", err)
	}
	records := make([]auditEventRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read ACP audit event: %w", err)
		}
		var record auditEventRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode ACP audit event: %w", err)
		}
		if record.RunID != runID {
			return nil, fmt.Errorf("ACP audit event run id mismatch: %q != %q", record.RunID, runID)
		}
		if err := validateAuditEvent(record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Seq < records[j].Seq })
	for index := 1; index < len(records); index++ {
		if records[index].Seq == records[index-1].Seq {
			return nil, fmt.Errorf("duplicate ACP audit event seq %d for run %s", records[index].Seq, runID)
		}
	}
	return records, nil
}

func (s *runJournalStore) pruneEventsLocked(runID string) error {
	dir := filepath.Join(s.runDir(runID), "events")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list ACP audit events for pruning: %w", err)
	}
	type candidate struct {
		name string
		seq  uint64
		size int64
	}
	items := make([]candidate, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		seq, ok := parseAuditEventFilename(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		items = append(items, candidate{name: entry.Name(), seq: seq, size: info.Size()})
		total += info.Size()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	for len(items) > maxAuditEventsPerRun || total > maxAuditBytesPerRun {
		oldest := items[0]
		if err := os.Remove(filepath.Join(dir, oldest.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune ACP audit event: %w", err)
		}
		total -= oldest.size
		items = items[1:]
	}
	return nil
}

func (s *runJournalStore) pruneRunsLocked() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("list ACP run journal for pruning: %w", err)
	}
	if len(entries) <= maxPersistedAuditRuns {
		return nil
	}
	type candidate struct {
		id        string
		updatedAt time.Time
	}
	settled := make([]candidate, 0, len(entries))
	runCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runCount++
		meta, err := s.readMetaLocked(entry.Name())
		if err != nil {
			return err
		}
		if meta.Status == RunRunning {
			continue
		}
		settled = append(settled, candidate{id: meta.RunID, updatedAt: meta.UpdatedAt})
	}
	if runCount <= maxPersistedAuditRuns {
		return nil
	}
	sort.Slice(settled, func(i, j int) bool {
		if settled[i].updatedAt.Equal(settled[j].updatedAt) {
			return settled[i].id < settled[j].id
		}
		return settled[i].updatedAt.Before(settled[j].updatedAt)
	})
	for _, item := range settled {
		if runCount <= maxPersistedAuditRuns {
			break
		}
		if err := os.RemoveAll(s.runDir(item.id)); err != nil {
			return fmt.Errorf("prune ACP run journal %s: %w", item.id, err)
		}
		runCount--
	}
	if runCount > maxPersistedAuditRuns {
		return fmt.Errorf("ACP run journal has %d runs but only running entries remain", runCount)
	}
	return nil
}

func runFromAudit(meta runAuditMeta, records []auditEventRecord, store *runJournalStore) (*Run, error) {
	run := newRun(meta.RunID, meta.SessionID)
	run.Status = meta.Status
	run.StartedAt = meta.StartedAt
	run.EndedAt = meta.EndedAt
	run.StopReason = meta.StopReason
	if meta.ErrorCode != "" {
		run.Err = &Error{Code: meta.ErrorCode}
	}
	run.persistEvent = store.Append
	run.events = make([]Event, 0, len(records))
	run.eventBytes = 0
	run.nextSeq = 1
	for _, record := range records {
		event := auditRecordToEvent(record)
		run.events = append(run.events, event)
		run.eventBytes += eventSize(event)
		if event.Seq >= run.nextSeq {
			run.nextSeq = event.Seq + 1
		}
	}
	if run.Status != RunRunning {
		run.finalizeOnce.Do(func() { close(run.finalized) })
	}
	return run, nil
}

func eventToAuditRecord(event Event) auditEventRecord {
	updateDigest := event.UpdateSHA256
	if updateDigest == "" && len(event.Update) > 0 {
		updateDigest = digestBytesSHA256(event.Update)
	}
	messageDigest := event.MessageSHA256
	if messageDigest == "" && event.Message != "" {
		messageDigest = digestStringSHA256(event.Message)
	}
	originalUpdateBytes := event.OriginalUpdateBytes
	if originalUpdateBytes == 0 && len(event.Update) > 0 {
		originalUpdateBytes = len(event.Update)
	}
	return auditEventRecord{
		SchemaVersion:       auditJournalSchemaVersion,
		Seq:                 event.Seq,
		Type:                event.Type,
		SessionID:           event.SessionID,
		RunID:               event.RunID,
		ToolCallID:          boundedAuditIdentity(event.ToolCallID),
		MessageID:           boundedAuditIdentity(event.MessageID),
		CreatedAt:           event.CreatedAt,
		UpdateSHA256:        updateDigest,
		UpdateTruncated:     event.UpdateTruncated,
		OriginalUpdateBytes: originalUpdateBytes,
		StopReason:          event.StopReason,
		ErrorCode:           event.ErrorCode,
		MessageSHA256:       messageDigest,
	}
}

func auditRecordToEvent(record auditEventRecord) Event {
	return Event{
		Seq:                 record.Seq,
		Type:                record.Type,
		SessionID:           record.SessionID,
		RunID:               record.RunID,
		ToolCallID:          record.ToolCallID,
		MessageID:           record.MessageID,
		CreatedAt:           record.CreatedAt,
		UpdateSHA256:        record.UpdateSHA256,
		UpdateOmitted:       record.UpdateSHA256 != "",
		UpdateTruncated:     record.UpdateTruncated,
		OriginalUpdateBytes: record.OriginalUpdateBytes,
		StopReason:          record.StopReason,
		ErrorCode:           record.ErrorCode,
		MessageSHA256:       record.MessageSHA256,
		MessageOmitted:      record.MessageSHA256 != "",
	}
}

func validateRunAuditMeta(meta runAuditMeta) error {
	if meta.SchemaVersion != auditJournalSchemaVersion {
		return fmt.Errorf("unsupported ACP run journal schema %d", meta.SchemaVersion)
	}
	if !validJournalIdentity(meta.RunID) || !validJournalIdentity(meta.SessionID) {
		return errors.New("invalid ACP run journal identity")
	}
	switch meta.Status {
	case RunRunning, RunCompleted, RunCancelled, RunInterrupted, RunFailed:
	default:
		return fmt.Errorf("invalid ACP run journal status %q", meta.Status)
	}
	if meta.StartedAt.IsZero() || meta.UpdatedAt.IsZero() {
		return errors.New("ACP run journal timestamps are required")
	}
	if meta.EndedAt != nil && meta.EndedAt.Before(meta.StartedAt) {
		return errors.New("ACP run journal ended_at precedes started_at")
	}
	if meta.MessageSHA256 != "" && !validDigestHex(meta.MessageSHA256) {
		return errors.New("invalid ACP run journal message digest")
	}
	return nil
}

func validateAuditEvent(record auditEventRecord) error {
	if record.SchemaVersion != auditJournalSchemaVersion {
		return fmt.Errorf("unsupported ACP audit event schema %d", record.SchemaVersion)
	}
	if record.Seq == 0 || strings.TrimSpace(record.Type) == "" ||
		!validJournalIdentity(record.RunID) || !validJournalIdentity(record.SessionID) ||
		record.CreatedAt.IsZero() {
		return errors.New("invalid ACP audit event identity")
	}
	if record.UpdateSHA256 != "" && !validDigestHex(record.UpdateSHA256) {
		return errors.New("invalid ACP audit update digest")
	}
	if record.MessageSHA256 != "" && !validDigestHex(record.MessageSHA256) {
		return errors.New("invalid ACP audit message digest")
	}
	return nil
}

func validJournalIdentity(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]byte(value)) <= maxAuditIdentityBytes &&
		!strings.ContainsAny(value, "/\\\r\n")
}

func validDigestHex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func boundedAuditIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	if len([]byte(value)) > maxAuditIdentityBytes {
		return ""
	}
	return value
}

func auditEventFilename(seq uint64) string {
	return fmt.Sprintf("%020d.json", seq)
}

func parseAuditEventFilename(name string) (uint64, bool) {
	if len(name) != 25 || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	seq, err := strconv.ParseUint(strings.TrimSuffix(name, ".json"), 10, 64)
	return seq, err == nil && seq > 0
}

func digestBytesSHA256(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256Sum(data)
	return hex.EncodeToString(sum)
}

func digestStringSHA256(value string) string {
	if value == "" {
		return ""
	}
	return digestBytesSHA256([]byte(value))
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
