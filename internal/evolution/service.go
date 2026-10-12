package evolution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

var ErrSelfProof = errors.New("rejected_self_proof")

type Service struct {
	config func() config.Config
	tasks  *taskstate.Store
	client client
}

func New(configProvider func() config.Config, tasks *taskstate.Store) *Service {
	service := &Service{config: configProvider, tasks: tasks}
	service.client = client{config: configProvider}
	return service
}

func (s *Service) Manage(ctx context.Context, req Request) (Result, error) {
	req.Intent = strings.ToLower(strings.TrimSpace(req.Intent))
	switch req.Intent {
	case "propose":
		return s.propose(ctx, req)
	case "assess":
		return s.assess(ctx, req)
	case "retract":
		return s.retire(ctx, req, "")
	case "supersede":
		return s.retire(ctx, req, strings.TrimSpace(req.SupersededBy))
	case "bind":
		return s.bind(ctx, req)
	default:
		return Result{}, fmt.Errorf("unsupported evolve intent %q", req.Intent)
	}
}

func (s *Service) Guidance(ctx context.Context, task taskstate.Task) ([]taskstate.EvolutionContextItem, error) {
	records, err := s.client.query(ctx, Query{Query: taskQueryText(task), Statuses: []string{StatusActive, StatusVerified}, Limit: 50})
	if err != nil {
		return nil, err
	}
	items := make([]taskstate.EvolutionContextItem, 0, 5)
	for _, record := range records {
		if !recordAppliesToTask(record, task) || taskWithholdsGuidance(task, record.EvolutionID) {
			continue
		}
		item := contextItem(record, true, "")
		items = append(items, item)
		if len(items) == 5 {
			break
		}
	}
	return items, nil
}

// EvidenceRecords performs an exact, read-only reverse lookup of Evolution records
// that consumed one persisted final-review evidence reference.
func (s *Service) EvidenceRecords(ctx context.Context, ref string) ([]Record, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 512 || strings.ContainsAny(ref, "\r\n") {
		return nil, errors.New("invalid evidence_ref")
	}
	records, err := s.client.query(ctx, Query{EvidenceRef: ref, Limit: 50})
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(records))
	for _, record := range records {
		for _, evidence := range record.Evidence {
			if evidence.Ref == ref {
				out = append(out, record)
				break
			}
		}
	}
	return out, nil
}

func (s *Service) Candidates(ctx context.Context, task taskstate.Task) ([]taskstate.EvolutionContextItem, error) {
	if task.FinalReview == nil || task.FinalReview.ReviewRevision == "" {
		return nil, nil
	}
	queryText := taskQueryText(task) + " " + strings.Join(task.FinalReview.VerifiedFacts, " ") + " " + strings.Join(task.FinalReview.OpenRisks, " ")
	records, err := s.client.query(ctx, Query{Query: queryText, Statuses: []string{StatusProvisional, StatusActive, StatusVerified, StatusQuarantine}, Limit: 50})
	if err != nil {
		return nil, err
	}
	evidenceRefs := reviewEvidenceRefs(task, true)
	guided := map[string]bool{}
	for _, item := range task.GuidanceContext {
		guided[item.EvolutionID] = true
	}
	items := make([]taskstate.EvolutionContextItem, 0, 5)
	for _, record := range records {
		if !recordAppliesToTask(record, task) {
			continue
		}
		item := contextItem(record, guided[record.EvolutionID], task.FinalReview.ReviewRevision)
		item.EvidenceRefs = append([]string(nil), evidenceRefs...)
		items = append(items, item)
		if len(items) == 5 {
			break
		}
	}
	return items, nil
}

// ResolveBindings 只消费执行前已经绑定的 learning check。final_review 的 pass/failed
// 只用于选择预声明的分支，本身不带 support/contradict 语义。
func (s *Service) ResolveBindings(ctx context.Context, task taskstate.Task) error {
	if task.FinalReview == nil || task.FinalReview.ReviewRevision == "" {
		return nil
	}
	evidenceRefs := reviewEvidenceRefs(task, true)
	var failures []string
	for _, binding := range task.EvolutionBindings {
		relation, err := relationForReview(binding, task.FinalReview.Status)
		if err != nil {
			failures = append(failures, binding.EvolutionID+": "+err.Error())
			continue
		}
		if relation == RelationNone {
			continue
		}
		_, err = s.assess(ctx, Request{
			Intent:         "assess",
			EvolutionID:    binding.EvolutionID,
			TaskID:         task.ID,
			ReviewRevision: task.FinalReview.ReviewRevision,
			Relation:       relation,
			EvidenceRefs:   evidenceRefs,
			Rationale:      "pre-bound learning check resolved from final_review",
		})
		if err != nil {
			failures = append(failures, binding.EvolutionID+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("resolve evolution bindings: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (s *Service) propose(ctx context.Context, req Request) (Result, error) {
	if req.Candidate == nil {
		return Result{}, errors.New("candidate is required for propose")
	}
	candidate, err := normalizeCandidate(*req.Candidate)
	if err != nil {
		return Result{}, err
	}
	// 只做精确 canonical_key/statement 去重，避免模型或模糊检索擅自语义合并。
	// 新记录使用稳定身份哈希；这样多个 AgentDock 节点并发提同一候选时会竞争同一个 CAS 路径，
	// 不需要 leader，也不会因为“先查后随机 ID”生成重复卡。查询仍保留，用于兼容旧的随机 ID 记录。
	dedupQuery := candidate.CanonicalKey
	if dedupQuery == "" {
		dedupQuery = candidate.Statement
	}
	records, err := s.client.query(ctx, Query{Query: dedupQuery, Limit: 50})
	if err != nil {
		return Result{}, err
	}
	for _, record := range records {
		if exactCandidateMatch(record, candidate) {
			return Result{Intent: "propose", Record: &record, Changed: false, Idempotent: true, Message: "exact candidate already exists"}, nil
		}
	}
	evolutionID := candidateEvolutionID(candidate)
	record := Record{
		EvolutionID:   evolutionID,
		Title:         candidate.Statement,
		Statement:     candidate.Statement,
		Type:          candidate.Type,
		Scope:         candidate.Scope,
		Project:       candidate.Project,
		Device:        candidate.Device,
		CanonicalKey:  candidate.CanonicalKey,
		Status:        initialStatus(candidate.Type, candidate.Source),
		PolicyVersion: PolicyVersion,
		Source:        candidate.Source,
		Tags:          append([]string(nil), candidate.Tags...),
	}
	transition, err := s.client.transition(ctx, newTransitionRequest(mustOpaqueID("op_"), 0, record, nil))
	if errors.Is(err, ErrRevisionConflict) {
		// 另一个节点可能刚创建了同一稳定 ID。重读后只在身份完全匹配时视为幂等；
		// 哈希碰撞或异常占位不能被静默吞掉。
		existing, queryErr := s.client.query(ctx, Query{EvolutionID: evolutionID, Limit: 1})
		if queryErr != nil {
			return Result{}, queryErr
		}
		if len(existing) == 1 && exactCandidateMatch(existing[0], candidate) {
			return Result{Intent: "propose", Record: &existing[0], Changed: false, Idempotent: true, Message: "exact candidate already exists"}, nil
		}
		return Result{}, err
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Intent: "propose", Record: &transition.Record, Changed: !transition.Idempotent, Idempotent: transition.Idempotent}, nil
}

func (s *Service) assess(ctx context.Context, req Request) (Result, error) {
	evolutionID := strings.TrimSpace(req.EvolutionID)
	if evolutionID == "" {
		return Result{}, errors.New("evolution_id is required for assess")
	}
	relation := strings.ToLower(strings.TrimSpace(req.Relation))
	if relation == RelationNotApplicable || relation == RelationUncertain {
		return Result{Intent: "assess", Relation: relation, Changed: false, Message: "assessment recorded as non-evidence"}, nil
	}
	if relation != "" && relation != RelationSupport && relation != RelationContradict {
		return Result{}, errors.New("relation must be support, contradict, not_applicable or uncertain")
	}

	task, err := s.tasks.Get(strings.TrimSpace(req.TaskID))
	if err != nil {
		return Result{}, err
	}
	if task.FinalReview == nil || task.FinalReview.ReviewRevision == "" || task.FinalReview.ReviewRevision != strings.TrimSpace(req.ReviewRevision) {
		return Result{}, errors.New("review_revision does not match current task final_review")
	}
	binding, ok := findEvolutionBinding(task, evolutionID)
	if !ok {
		return Result{}, errors.New("evolution was not bound to this task before execution")
	}
	expectedRelation, err := relationForReview(binding, task.FinalReview.Status)
	if err != nil {
		return Result{}, err
	}
	if expectedRelation == RelationNone {
		return Result{}, errors.New("pre-bound learning check resolves to none for this task outcome")
	}
	if relation == "" {
		relation = expectedRelation
	}
	if relation != expectedRelation {
		return Result{}, fmt.Errorf("relation %s does not match pre-bound task outcome %s", relation, expectedRelation)
	}
	if relation == RelationSupport && (taskGuided(task, evolutionID) || taskDerivedFromEvolution(task, evolutionID)) {
		return Result{}, ErrSelfProof
	}
	if err := validateEvidenceRefs(task, req.EvidenceRefs); err != nil {
		return Result{}, err
	}

	operationID := mustOpaqueID("op_")
	for attempt := 0; attempt < 3; attempt++ {
		record, err := s.get(ctx, evolutionID)
		if err != nil {
			return Result{}, err
		}
		if record.Status == StatusRetired {
			return Result{}, errors.New("retired evolution record cannot be assessed")
		}
		if !recordAppliesToTask(record, task) {
			return Result{}, errors.New("evolution record scope does not apply to this task")
		}
		if existingRelation := existingAssessment(record, task.ID); existingRelation != "" {
			if existingRelation == relation {
				return Result{Intent: "assess", Record: &record, Relation: relation, Changed: false, Idempotent: true}, nil
			}
			return Result{}, fmt.Errorf("task review already assessed as %s", existingRelation)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, ref := range req.EvidenceRefs {
			record.Evidence = append(record.Evidence, Evidence{Ref: ref, Relation: relation, TaskID: task.ID, ReviewRevision: task.FinalReview.ReviewRevision, Rationale: strings.TrimSpace(req.Rationale), RecordedAt: now})
		}
		if relation == RelationSupport {
			record.SupportCount++
		} else {
			record.ContradictCount++
		}
		record.Status = nextStatus(record, relation)
		record.PolicyVersion = PolicyVersion
		transition, err := s.client.transition(ctx, newTransitionRequest(operationID, record.Revision, record, req.EvidenceRefs))
		if errors.Is(err, ErrRevisionConflict) {
			continue
		}
		if err != nil {
			return Result{}, err
		}
		return Result{Intent: "assess", Record: &transition.Record, Relation: relation, Changed: !transition.Idempotent, Idempotent: transition.Idempotent}, nil
	}
	return Result{}, ErrRevisionConflict
}

func (s *Service) retire(ctx context.Context, req Request, supersededBy string) (Result, error) {
	evolutionID := strings.TrimSpace(req.EvolutionID)
	if evolutionID == "" {
		return Result{}, errors.New("evolution_id is required")
	}
	operationID := mustOpaqueID("op_")
	for attempt := 0; attempt < 3; attempt++ {
		record, err := s.get(ctx, evolutionID)
		if err != nil {
			return Result{}, err
		}
		if record.Status == StatusRetired && (supersededBy == "" || record.SupersededBy == supersededBy) {
			return Result{Intent: req.Intent, Record: &record, Changed: false, Idempotent: true}, nil
		}
		record.Status = StatusRetired
		record.SupersededBy = supersededBy
		record.PolicyVersion = PolicyVersion
		transition, err := s.client.transition(ctx, newTransitionRequest(operationID, record.Revision, record, nil))
		if errors.Is(err, ErrRevisionConflict) {
			continue
		}
		if err != nil {
			return Result{}, err
		}
		return Result{Intent: req.Intent, Record: &transition.Record, Changed: true}, nil
	}
	return Result{}, ErrRevisionConflict
}

// ValidateBindings validates create-time learning checks before a Task is persisted.
// A support-bearing check is an explicit blinded validation: the target Evolution will be
// withheld from this Task's Guidance for its whole lifetime, while the normal anti-self-proof
// rule still rejects any target the Task has already seen or derives from.
func (s *Service) ValidateBindings(ctx context.Context, task taskstate.Task, bindings []taskstate.EvolutionBinding) ([]taskstate.EvolutionBinding, error) {
	if len(bindings) > 3 {
		return nil, errors.New("learning_checks cannot exceed 3")
	}
	out := make([]taskstate.EvolutionBinding, 0, len(bindings))
	seen := make(map[string]taskstate.EvolutionBinding, len(bindings))
	for _, binding := range bindings {
		_, normalized, err := s.validateBinding(ctx, task, binding.EvolutionID, &LearningCheck{OnSuccess: binding.OnSuccess, OnFailure: binding.OnFailure})
		if err != nil {
			return nil, err
		}
		if existing, ok := seen[normalized.EvolutionID]; ok {
			if existing.OnSuccess == normalized.OnSuccess && existing.OnFailure == normalized.OnFailure {
				continue
			}
			return nil, errors.New("evolution is already bound with different learning check semantics")
		}
		seen[normalized.EvolutionID] = normalized
		out = append(out, normalized)
	}
	return out, nil
}

func (s *Service) validateBinding(ctx context.Context, task taskstate.Task, evolutionID string, learningCheck *LearningCheck) (Record, taskstate.EvolutionBinding, error) {
	if strings.TrimSpace(evolutionID) == "" {
		return Record{}, taskstate.EvolutionBinding{}, errors.New("evolution_id is required")
	}
	check, err := normalizeLearningCheck(learningCheck)
	if err != nil {
		return Record{}, taskstate.EvolutionBinding{}, err
	}
	record, err := s.get(ctx, strings.TrimSpace(evolutionID))
	if err != nil {
		return Record{}, taskstate.EvolutionBinding{}, err
	}
	if record.Status == StatusRetired {
		return Record{}, taskstate.EvolutionBinding{}, errors.New("retired evolution record cannot be bound")
	}
	if !recordAppliesToTask(record, task) {
		return Record{}, taskstate.EvolutionBinding{}, errors.New("evolution record scope does not apply to this task")
	}
	if (check.OnSuccess == RelationSupport || check.OnFailure == RelationSupport) && (taskGuided(task, record.EvolutionID) || taskDerivedFromEvolution(task, record.EvolutionID)) {
		return Record{}, taskstate.EvolutionBinding{}, ErrSelfProof
	}
	return record, taskstate.EvolutionBinding{EvolutionID: record.EvolutionID, OnSuccess: check.OnSuccess, OnFailure: check.OnFailure}, nil
}

func (s *Service) bind(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.TaskID) == "" || strings.TrimSpace(req.EvolutionID) == "" {
		return Result{}, errors.New("task_id and evolution_id are required for bind")
	}
	task, err := s.tasks.Get(strings.TrimSpace(req.TaskID))
	if err != nil {
		return Result{}, err
	}
	record, binding, err := s.validateBinding(ctx, task, req.EvolutionID, req.LearningCheck)
	if err != nil {
		return Result{}, err
	}
	for _, existing := range task.EvolutionBindings {
		if existing.EvolutionID != record.EvolutionID {
			continue
		}
		if existing.OnSuccess == binding.OnSuccess && existing.OnFailure == binding.OnFailure {
			return Result{Intent: "bind", Changed: false, Idempotent: true, Message: "learning check already bound"}, nil
		}
		return Result{}, errors.New("evolution is already bound with different learning check semantics")
	}
	if _, err := s.tasks.BindEvolution(req.TaskID, binding); err != nil {
		return Result{}, err
	}
	return Result{Intent: "bind", Changed: true, Message: "pre-execution learning check stored on task"}, nil
}

func (s *Service) get(ctx context.Context, evolutionID string) (Record, error) {
	records, err := s.client.query(ctx, Query{EvolutionID: strings.TrimSpace(evolutionID), Limit: 2})
	if err != nil {
		return Record{}, err
	}
	if len(records) != 1 {
		if len(records) == 0 {
			return Record{}, errors.New("evolution record not found")
		}
		return Record{}, errors.New("evolution_id is not unique")
	}
	return records[0], nil
}

func normalizeCandidate(candidate Candidate) (Candidate, error) {
	candidate.Type = strings.ToLower(strings.TrimSpace(candidate.Type))
	candidate.Statement = strings.TrimSpace(candidate.Statement)
	candidate.Scope = strings.ToLower(strings.TrimSpace(candidate.Scope))
	candidate.Project = strings.ToLower(strings.TrimSpace(candidate.Project))
	candidate.Device = strings.TrimSpace(candidate.Device)
	candidate.CanonicalKey = strings.TrimSpace(candidate.CanonicalKey)
	candidate.Source = strings.TrimSpace(candidate.Source)
	if candidate.Type == "" || candidate.Statement == "" {
		return Candidate{}, errors.New("candidate type and statement are required")
	}
	if !allowedCandidateType(candidate.Type) {
		return Candidate{}, fmt.Errorf("unsupported candidate type %q", candidate.Type)
	}
	if candidate.Scope == "" {
		candidate.Scope = "project"
	}
	if candidate.Project == "" {
		candidate.Project = "global"
	}
	if len([]rune(candidate.Statement)) > 2000 {
		return Candidate{}, errors.New("candidate statement is too long")
	}
	return candidate, nil
}

func exactCandidateMatch(record Record, candidate Candidate) bool {
	identityMatch := record.Type == candidate.Type && record.Scope == candidate.Scope && record.Project == candidate.Project && record.Device == candidate.Device
	canonicalMatch := candidate.CanonicalKey != "" && record.CanonicalKey == candidate.CanonicalKey
	statementMatch := candidate.CanonicalKey == "" && record.Statement == candidate.Statement
	return identityMatch && (canonicalMatch || statementMatch)
}

func candidateEvolutionID(candidate Candidate) string {
	identity := candidate.CanonicalKey
	if identity == "" {
		identity = candidate.Statement
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{candidate.Type, candidate.Scope, candidate.Project, candidate.Device, identity}, "\x00")))
	return "evo_" + hex.EncodeToString(sum[:16])
}

func initialStatus(kind, source string) string {
	// Stage 3 的外部模型只有提案权。即使它把候选标成 preference/decision，
	// 也不能借类型分流直接获得 active 权限。
	kind = strings.ToLower(strings.TrimSpace(kind))
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "nexus-stage3") || kind == "workflow_template" || kind == "skill" {
		return StatusProvisional
	}
	switch kind {
	case "preference", "decision", "constraint", "explicit_decision", "user_preference":
		return StatusActive
	default:
		return StatusProvisional
	}
}

func nextStatus(record Record, relation string) string {
	if relation == RelationContradict {
		return StatusQuarantine
	}
	if record.ContradictCount > 0 {
		return StatusQuarantine
	}
	if record.SupportCount >= 3 {
		return StatusVerified
	}
	if record.SupportCount >= 2 {
		return StatusActive
	}
	return record.Status
}

func allowedCandidateType(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "preference", "user_preference", "decision", "explicit_decision", "constraint",
		"runbook", "bug_pattern", "deploy_note", "project_trap", "architecture", "anti_pattern",
		"operational_lesson", "technical_fact", "workflow_template", "skill":
		return true
	default:
		return false
	}
}

func newTransitionRequest(operationID string, expectedRevision int64, record Record, evidenceRefs []string) transitionRequest {
	return transitionRequest{
		OperationID:      operationID,
		ExpectedRevision: expectedRevision,
		PolicyVersion:    record.PolicyVersion,
		NextState:        record.Status,
		EvidenceRefs:     append([]string(nil), evidenceRefs...),
		Record:           record,
	}
}

func recordAppliesToTask(record Record, task taskstate.Task) bool {
	project := strings.ToLower(strings.TrimSpace(task.Project))
	recordProject := strings.ToLower(strings.TrimSpace(record.Project))
	if recordProject == "" {
		recordProject = "global"
	}
	if recordProject != "global" && (project == "" || recordProject != project) {
		return false
	}
	device := strings.TrimSpace(task.Device)
	recordDevice := strings.TrimSpace(record.Device)
	if recordDevice != "" && (device == "" || !strings.EqualFold(recordDevice, device)) {
		return false
	}
	return true
}

func taskQueryText(task taskstate.Task) string {
	parts := []string{task.Title, task.Goal}
	for _, condition := range task.Conditions {
		parts = append(parts, condition.Text)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func verifiedEvidenceRefs(task taskstate.Task) []string {
	return reviewEvidenceRefs(task, false)
}

func reviewEvidenceRefs(task taskstate.Task, includeRisks bool) []string {
	if task.FinalReview == nil {
		return nil
	}
	refs := make([]string, 0, len(task.FinalReview.VerifiedFacts)+len(task.FinalReview.OpenRisks)+len(task.FinalReview.MissingChecks))
	for i := range task.FinalReview.VerifiedFacts {
		refs = append(refs, fmt.Sprintf("task:%s:review:%s:verified:%d", task.ID, task.FinalReview.ReviewRevision, i))
	}
	if includeRisks {
		for i := range task.FinalReview.OpenRisks {
			refs = append(refs, fmt.Sprintf("task:%s:review:%s:risk:%d", task.ID, task.FinalReview.ReviewRevision, i))
		}
		for i := range task.FinalReview.MissingChecks {
			refs = append(refs, fmt.Sprintf("task:%s:review:%s:missing:%d", task.ID, task.FinalReview.ReviewRevision, i))
		}
	}
	return refs
}

func validateEvidenceRefs(task taskstate.Task, refs []string) error {
	if len(refs) == 0 {
		return errors.New("at least one evidence_ref is required")
	}
	allowed := map[string]bool{}
	for _, ref := range reviewEvidenceRefs(task, true) {
		allowed[ref] = true
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !allowed[ref] {
			return fmt.Errorf("evidence_ref %q does not belong to the current final_review", ref)
		}
		if seen[ref] {
			return fmt.Errorf("duplicate evidence_ref %q", ref)
		}
		seen[ref] = true
	}
	return nil
}

func normalizeLearningCheck(check *LearningCheck) (LearningCheck, error) {
	if check == nil {
		return LearningCheck{}, errors.New("learning_check is required for bind")
	}
	normalized := LearningCheck{
		OnSuccess: strings.ToLower(strings.TrimSpace(check.OnSuccess)),
		OnFailure: strings.ToLower(strings.TrimSpace(check.OnFailure)),
	}
	if !validLearningRelation(normalized.OnSuccess) || !validLearningRelation(normalized.OnFailure) {
		return LearningCheck{}, errors.New("learning_check outcomes must be support, contradict or none")
	}
	if normalized.OnSuccess == RelationNone && normalized.OnFailure == RelationNone {
		return LearningCheck{}, errors.New("learning_check must produce evidence for at least one outcome")
	}
	return normalized, nil
}

func validLearningRelation(value string) bool {
	switch value {
	case RelationSupport, RelationContradict, RelationNone:
		return true
	default:
		return false
	}
}

func findEvolutionBinding(task taskstate.Task, evolutionID string) (taskstate.EvolutionBinding, bool) {
	for _, binding := range task.EvolutionBindings {
		if strings.TrimSpace(binding.EvolutionID) == evolutionID {
			return binding, true
		}
	}
	return taskstate.EvolutionBinding{}, false
}

func relationForReview(binding taskstate.EvolutionBinding, reviewStatus string) (string, error) {
	var relation string
	switch strings.ToLower(strings.TrimSpace(reviewStatus)) {
	case taskstate.FinalReviewPass:
		relation = strings.ToLower(strings.TrimSpace(binding.OnSuccess))
	case taskstate.FinalReviewFailed:
		relation = strings.ToLower(strings.TrimSpace(binding.OnFailure))
	default:
		return "", fmt.Errorf("unsupported final_review status %q", reviewStatus)
	}
	if !validLearningRelation(relation) {
		return "", errors.New("stored learning check has invalid outcome semantics")
	}
	return relation, nil
}

func taskWithholdsGuidance(task taskstate.Task, evolutionID string) bool {
	for _, binding := range task.EvolutionBindings {
		if strings.TrimSpace(binding.EvolutionID) != strings.TrimSpace(evolutionID) {
			continue
		}
		if binding.OnSuccess == RelationSupport || binding.OnFailure == RelationSupport {
			return true
		}
	}
	return false
}

func taskGuided(task taskstate.Task, evolutionID string) bool {
	for _, seenID := range task.EvolutionGuidanceSeen {
		if seenID == evolutionID {
			return true
		}
	}
	return false
}

func taskDerivedFromEvolution(task taskstate.Task, evolutionID string) bool {
	for _, template := range task.SourceTemplates {
		if strings.TrimSpace(template.SourceEvolutionID) == evolutionID {
			return true
		}
	}
	return false
}

func existingAssessment(record Record, taskID string) string {
	for _, evidence := range record.Evidence {
		if evidence.TaskID == taskID {
			return evidence.Relation
		}
	}
	return ""
}

func newOpaqueID(prefix string) (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

func mustOpaqueID(prefix string) string {
	value, err := newOpaqueID(prefix)
	if err == nil {
		return value
	}
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 16)
}
